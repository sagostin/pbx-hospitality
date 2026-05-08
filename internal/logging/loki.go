package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sagostin/pbx-hospitality/internal/config"
)

type LokiEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}

type LokiStream struct {
	Labels  string      `json:"labels"`
	Entries []LokiEntry `json:"entries"`
}

type LokiPushRequest struct {
	Streams []LokiStream `json:"streams"`
}

type LokiWriter struct {
	url     string
	enabled bool
	client  *http.Client
	host    string
	service string
	env     string
	mu      sync.Mutex
}

func NewLokiWriter(cfg config.LoggingConfig) (*LokiWriter, error) {
	if !cfg.LokiEnabled || cfg.LokiURL == "" {
		log.Info().Msg("Loki logging disabled")
		return &LokiWriter{enabled: false}, nil
	}

	host := os.Getenv("HOSTNAME")
	if host == "" {
		if hostname, err := os.Hostname(); err == nil {
			host = hostname
		}
	}
	if host == "" {
		host = "unknown"
	}

	service := os.Getenv("SERVICE_NAME")
	if service == "" {
		service = "bicom-hospitality"
	}

	env := getEnv()

	writer := &LokiWriter{
		url:     cfg.LokiURL,
		enabled: true,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
		host:    host,
		service: service,
		env:     env,
	}

	log.Info().
		Str("loki_url", cfg.LokiURL).
		Int("batch_size", cfg.LokiBatchSize).
		Int("batch_wait_secs", cfg.LokiBatchWait).
		Str("service", service).
		Str("env", env).
		Str("host", host).
		Msg("Loki logging enabled")

	return writer, nil
}

func getEnv() string {
	if e := os.Getenv("ENV"); e != "" {
		return e
	}
	if e := os.Getenv("ENVIRONMENT"); e != "" {
		return e
	}
	if os.Getenv("PRODUCTION") == "true" {
		return "production"
	}
	if os.Getenv("DEV") == "true" || os.Getenv("DEVELOPMENT") == "true" {
		return "development"
	}
	return "unknown"
}

func (w *LokiWriter) Write(p []byte) (n int, err error) {
	if !w.enabled {
		return len(p), nil
	}

	logLine := string(p)
	level := extractLevel(logLine)

	labels := fmt.Sprintf(`{service="%s", env="%s", host="%s", source="local", level="%s"}`,
		w.service, w.env, w.host, level)

	req := LokiPushRequest{
		Streams: []LokiStream{
			{
				Labels:  labels,
				Entries: []LokiEntry{{Timestamp: time.Now(), Line: logLine}},
			},
		},
	}

	go w.sendAsync(req)

	return len(p), nil
}

func (w *LokiWriter) sendAsync(req LokiPushRequest) {
	w.mu.Lock()
	defer w.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal Loki request")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(data))
	if err != nil {
		log.Error().Err(err).Msg("Failed to create Loki request")
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(httpReq)
	if err != nil {
		log.Warn().Err(err).Str("loki_url", w.url).Msg("Failed to send logs to Loki")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Warn().Int("status", resp.StatusCode).Msg("Loki returned error")
	}
}

func (w *LokiWriter) SendLog(level, message string, fields map[string]interface{}) error {
	if !w.enabled {
		return nil
	}

	labels := fmt.Sprintf(`{service="%s", env="%s", host="%s", source="local", level="%s"}`,
		w.service, w.env, w.host, level)

	entry := LokiEntry{
		Timestamp: time.Now(),
		Line:      formatLogLine(level, message, fields),
	}

	req := LokiPushRequest{
		Streams: []LokiStream{
			{
				Labels:  labels,
				Entries: []LokiEntry{entry},
			},
		},
	}

	go w.sendAsync(req)
	return nil
}

func (w *LokiWriter) SendRemoteLog(siteID, level, message string, fields map[string]interface{}) error {
	if !w.enabled {
		return nil
	}

	labels := fmt.Sprintf(`{service="%s", env="%s", host="%s", source="remote", site_id="%s", level="%s"}`,
		w.service, w.env, w.host, siteID, level)

	entry := LokiEntry{
		Timestamp: time.Now(),
		Line:      formatLogLine(level, message, fields),
	}

	req := LokiPushRequest{
		Streams: []LokiStream{
			{
				Labels:  labels,
				Entries: []LokiEntry{entry},
			},
		},
	}

	go w.sendAsync(req)
	return nil
}

func formatLogLine(level, message string, fields map[string]interface{}) string {
	if len(fields) == 0 {
		return fmt.Sprintf(`{"level":"%s","message":"%s"}`, level, message)
	}
	f, _ := json.Marshal(fields)
	return fmt.Sprintf(`{"level":"%s","message":"%s","fields":%s}`, level, message, string(f))
}

func (w *LokiWriter) Enabled() bool {
	return w.enabled
}

func (w *LokiWriter) Close() error {
	return nil
}

func extractLevel(logLine string) string {
	for _, tag := range []string{"level=", "\"level\":\"", "level="} {
		if idx := indexOfIgnoreCase(logLine, tag); idx >= 0 {
			start := idx + len(tag)
			for i := start; i < len(logLine) && i < start+10; i++ {
				c := logLine[i]
				if c == '"' || c == ' ' || c == '\n' || c == ',' {
					return logLine[start:i]
				}
			}
		}
	}
	return "info"
}

func indexOfIgnoreCase(s, substr string) int {
	if len(substr) > len(s) {
		return -1
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if equalFold(s[i:i+len(substr)], substr) {
			return i
		}
	}
	return -1
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ac := a[i]
		bc := b[i]
		if ac >= 'A' && ac <= 'Z' {
			ac += 32
		}
		if bc >= 'A' && bc <= 'Z' {
			bc += 32
		}
		if ac != bc {
			return false
		}
	}
	return true
}
