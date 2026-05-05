package output

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

type OutputConfig struct {
	URL                 string
	UseWebsocket        bool
	BufferEnabled       bool
	BufferDir           string
	BufferMaxSize       int64
	BatchEnabled        bool
	BatchSize           int
	BatchTimeout        time.Duration
	BackpressureEnabled bool
}

type ResilientOutput struct {
	cfg       OutputConfig
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	conn      net.Conn
	wsConn    *websocket.Conn
	wsDialer  websocket.Dialer
	closed    bool
	buffer    *bufferQueue
	batch     []EventEnvelope
}

type EventEnvelope struct {
	Protocol string                 `json:"protocol"`
	Event    map[string]interface{} `json:"event"`
	SentAt   time.Time              `json:"sent_at,omitempty"`
}

func NewResilientOutput(cfg OutputConfig) (*ResilientOutput, error) {
	if cfg.BufferDir != "" && cfg.BufferEnabled {
		bq, err := newBufferQueue(cfg.BufferDir, cfg.BufferMaxSize)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize buffer queue: %w", err)
		}
		log.Info().Str("dir", cfg.BufferDir).Msg("Local buffering enabled")
		_ = bq
	}

	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.BatchTimeout == 0 {
		cfg.BatchTimeout = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &ResilientOutput{
		cfg:    cfg,
		ctx:    ctx,
		cancel: cancel,
		batch:  make([]EventEnvelope, 0, cfg.BatchSize),
	}, nil
}

func (o *ResilientOutput) connect() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return fmt.Errorf("output is closed")
	}

	var err error
	if o.cfg.UseWebsocket {
		u := url.URL{Scheme: "wss", Host: o.cfg.URL, Path: "/"}
		o.wsConn, _, err = o.wsDialer.Dial(u.String(), nil)
	} else {
		o.conn, err = net.DialTimeout("tcp", o.cfg.URL, 10*time.Second)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", o.cfg.URL, err)
	}

	log.Info().Str("url", o.cfg.URL).Bool("websocket", o.cfg.UseWebsocket).Msg("Connected to downstream")
	return nil
}

func (o *ResilientOutput) Write(evt EventEnvelope) error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return fmt.Errorf("output is closed")
	}
	o.mu.Unlock()

	if o.cfg.BackpressureEnabled {
		return o.writeWithBackpressure(evt)
	}
	return o.writeNonBlocking(evt)
}

func (o *ResilientOutput) writeWithBackpressure(evt EventEnvelope) error {
	for {
		select {
		case <-o.ctx.Done():
			return o.bufferOrDrop(evt)
		default:
		}

		o.mu.Lock()
		connected := o.conn != nil
		o.mu.Unlock()

		if !connected {
			if err := o.connect(); err != nil {
				log.Warn().Err(err).Msg("Reconnect failed, buffering")
				time.Sleep(5 * time.Second)
				continue
			}
		}

		if o.cfg.BatchEnabled {
			return o.addToBatch(evt)
		}
		return o.sendEvent(evt)
	}
}

func (o *ResilientOutput) writeNonBlocking(evt EventEnvelope) error {
	if o.buffer != nil && !o.isConnected() {
		return o.buffer.Write(evt)
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.cfg.BatchEnabled {
		o.batch = append(o.batch, evt)
		if len(o.batch) >= o.cfg.BatchSize {
			return o.flushBatch()
		}
		return nil
	}

	return o.sendEventLocked(evt)
}

func (o *ResilientOutput) addToBatch(evt EventEnvelope) error {
	o.mu.Lock()
	o.batch = append(o.batch, evt)
	if len(o.batch) >= o.cfg.BatchSize {
		err := o.flushBatchLocked()
		o.mu.Unlock()
		return err
	}
	o.mu.Unlock()

	go o.flushBatchTimer()
	return nil
}

func (o *ResilientOutput) flushBatchTimer() {
	time.Sleep(o.cfg.BatchTimeout)
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.batch) > 0 {
		o.flushBatchLocked()
	}
}

func (o *ResilientOutput) flushBatch() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.flushBatchLocked()
}

func (o *ResilientOutput) flushBatchLocked() error {
	if len(o.batch) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, evt := range o.batch {
		data, err := json.Marshal(evt)
		if err != nil {
			log.Error().Err(err).Msg("Failed to marshal event in batch")
			continue
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}

	o.batch = o.batch[:0]

	if o.cfg.UseWebsocket {
		if o.wsConn == nil {
			if o.buffer != nil {
				return o.buffer.WriteBatch(buf.Bytes())
			}
			return fmt.Errorf("not connected and no buffer configured")
		}
		o.wsConn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		err := o.wsConn.WriteMessage(websocket.TextMessage, buf.Bytes())
		if err != nil {
			log.Warn().Err(err).Msg("Failed to send batch via websocket, buffering")
			if o.buffer != nil {
				o.buffer.WriteBatch(buf.Bytes())
			}
			o.wsConn.Close()
			o.wsConn = nil
			return err
		}
		return nil
	}

	if o.conn == nil {
		if o.buffer != nil {
			return o.buffer.WriteBatch(buf.Bytes())
		}
		return fmt.Errorf("not connected and no buffer configured")
	}

	o.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err := o.conn.Write(buf.Bytes())
	if err != nil {
		log.Warn().Err(err).Msg("Failed to send batch, buffering")
		if o.buffer != nil {
			o.buffer.WriteBatch(buf.Bytes())
		}
		o.mu.Lock()
		o.conn.Close()
		o.conn = nil
		o.mu.Unlock()
		return err
	}

	log.Debug().Msg("Batch sent")
	return nil
}

func (o *ResilientOutput) sendEvent(evt EventEnvelope) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.sendEventLocked(evt)
}

func (o *ResilientOutput) sendEventLocked(evt EventEnvelope) error {
	var conn net.Conn
	if o.cfg.UseWebsocket {
		conn = nil
	} else {
		conn = o.conn
	}

	if conn == nil && o.wsConn == nil {
		if o.buffer != nil {
			return o.buffer.Write(evt)
		}
		return fmt.Errorf("not connected and no buffer configured")
	}

	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}
	data = append(data, '\n')

	if o.cfg.UseWebsocket {
		o.wsConn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		err = o.wsConn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to send event via websocket, buffering")
			if o.buffer != nil {
				o.buffer.Write(evt)
			}
			o.wsConn.Close()
			o.wsConn = nil
			return err
		}
		return nil
	}

	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err = conn.Write(data)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to send event, buffering")
		if o.buffer != nil {
			o.buffer.Write(evt)
		}
		conn.Close()
		o.conn = nil
		return err
	}

	return nil
}

func (o *ResilientOutput) isConnected() bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.cfg.UseWebsocket {
		if o.wsConn == nil {
			return false
		}
		o.wsConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		_, _, err := o.wsConn.ReadMessage()
		if err != nil {
			o.wsConn.Close()
			o.wsConn = nil
			return false
		}
		return true
	}

	if o.conn == nil {
		return false
	}
	one := []byte{1}
	o.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_, err := o.conn.Write(one)
	if err != nil {
		o.conn.Close()
		o.conn = nil
		return false
	}
	return true
}

func (o *ResilientOutput) bufferOrDrop(evt EventEnvelope) error {
	if o.buffer != nil {
		return o.buffer.Write(evt)
	}
	log.Warn().Msg("Output closed, event dropped")
	return fmt.Errorf("output closed, event dropped")
}

func (o *ResilientOutput) Close() error {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil
	}
	o.closed = true
	o.mu.Unlock()

	o.cancel()

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.wsConn != nil {
		if len(o.batch) > 0 {
			o.flushBatchLocked()
		}
		o.wsConn.Close()
		o.wsConn = nil
	}

	if o.conn != nil {
		if len(o.batch) > 0 {
			o.flushBatchLocked()
		}
		o.conn.Close()
		o.conn = nil
	}

	if o.buffer != nil {
		o.buffer.Close()
	}

	return nil
}

type bufferQueue struct {
	mu      sync.Mutex
	dir     string
	maxSize int64
	curSize int64
	files   []string
}

func newBufferQueue(dir string, maxSize int64) (*bufferQueue, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &bufferQueue{
		dir:     dir,
		maxSize: maxSize,
		files:   []string{},
	}, nil
}

func (b *bufferQueue) Write(evt EventEnvelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return b.writeData(data)
}

func (b *bufferQueue) WriteBatch(data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writeData(data)
}

func (b *bufferQueue) writeData(data []byte) error {
	if b.maxSize > 0 && b.curSize+int64(len(data)) > b.maxSize {
		b.rotateOrEvict()
	}

	filename := fmt.Sprintf("%s/%d.buf", b.dir, time.Now().UnixNano())
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return err
	}

	b.curSize += int64(len(data))
	b.files = append(b.files, filename)
	return nil
}

func (b *bufferQueue) rotateOrEvict() {
	if len(b.files) == 0 {
		return
	}
	oldest := b.files[0]
	info, err := os.Stat(oldest)
	if err == nil {
		b.curSize -= info.Size()
	}
	os.Remove(oldest)
	b.files = b.files[1:]
}

func (b *bufferQueue) ReadAll() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var result []byte
	for _, fname := range b.files {
		data, err := os.ReadFile(fname)
		if err != nil {
			continue
		}
		result = append(result, data...)
	}
	return result, nil
}

func (b *bufferQueue) Close() error {
	for _, f := range b.files {
		os.Remove(f)
	}
	return nil
}

func (b *bufferQueue) replay(w io.Writer) error {
	data, err := b.ReadAll()
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var evt EventEnvelope
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		evt.SentAt = time.Now()
		out, _ := json.Marshal(evt)
		w.Write(out)
		w.Write([]byte("\n"))
	}
	return nil
}

func (o *ResilientOutput) Replay() error {
	if o.buffer == nil {
		return fmt.Errorf("no buffer configured")
	}

	if err := o.connect(); err != nil {
		return fmt.Errorf("failed to connect for replay: %w", err)
	}

	data, err := o.buffer.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read buffered events: %w", err)
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.conn.SetWriteDeadline(time.Now().Add(5 * time.Minute))
	_, err = o.conn.Write(data)
	if err != nil {
		return fmt.Errorf("failed to replay buffered events: %w", err)
	}

	log.Info().Int("bytes", len(data)).Msg("Buffered events replayed")
	return nil
}

type TLSOutput struct {
	Addr      string
	CertFile  string
	KeyFile   string
	CAFile    string
	ServerName string
}

func (o *TLSOutput) Dial() (*tls.Conn, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if o.CertFile != "" && o.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if o.CAFile != "" {
		// Client certificate authentication would be configured here
	}

	if o.ServerName != "" {
		tlsConfig.ServerName = o.ServerName
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", o.Addr, tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("TLS dial failed: %w", err)
	}

	return conn, nil
}