package logging

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sagostin/pbx-hospitality/internal/config"
)

var (
	globalLokiWriter *LokiWriter
)

func Init(cfg config.LoggingConfig) error {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	lokiWriter, err := NewLokiWriter(cfg)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to initialize Loki writer, continuing without Loki")
		lokiWriter = &LokiWriter{enabled: false}
	}
	globalLokiWriter = lokiWriter

	if lokiWriter.Enabled() {
		writers := []io.Writer{
			zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			lokiWriter,
		}
		log.Logger = zerolog.New(io.MultiWriter(writers...)).With().Timestamp().Logger()
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	level := parseLevel(cfg.Level)
	zerolog.SetGlobalLevel(level)

	log.Info().Str("level", cfg.Level).Msg("Logging initialized")
	return nil
}

func InitFromEnv() error {
	cfg := config.LoggingConfig{
		Level:         getEnvOrDefault("LOG_LEVEL", "info"),
		Format:        getEnvOrDefault("LOG_FORMAT", "json"),
		LokiEnabled:   os.Getenv("LOKI_ENABLED") == "true",
		LokiURL:       os.Getenv("LOKI_ENDPOINT"),
		LokiBatchSize: 100,
		LokiBatchWait: 5,
	}
	return Init(cfg)
}

func parseLevel(level string) zerolog.Level {
	switch level {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}

func getEnvOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func GetLokiWriter() *LokiWriter {
	return globalLokiWriter
}

func SendRemoteLog(siteID, level, message string, fields map[string]interface{}) error {
	if globalLokiWriter != nil {
		return globalLokiWriter.SendRemoteLog(siteID, level, message, fields)
	}
	return nil
}
