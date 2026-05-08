package websocket

import (
	"encoding/json"

	fiberws "github.com/gofiber/websocket/v2"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/logging"
)

type LogMessage struct {
	Type      string                 `json:"type"`
	SiteID    string                 `json:"site_id"`
	Timestamp int64                  `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

type LogSink struct {
	clients map[*fiberws.Conn]bool
}

func NewLogSink() *LogSink {
	return &LogSink{
		clients: make(map[*fiberws.Conn]bool),
	}
}

func (ls *LogSink) HandleWS(conn *fiberws.Conn) {
	ls.clients[conn] = true
	log.Info().Str("remote", conn.RemoteAddr().String()).Msg("Site connector connected to log sink")

	defer func() {
		delete(ls.clients, conn)
		conn.Close()
		log.Info().Str("remote", conn.RemoteAddr().String()).Msg("Site connector disconnected from log sink")
	}()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if fiberws.IsUnexpectedCloseError(err, fiberws.CloseGoingAway, fiberws.CloseAbnormalClosure) {
				log.Warn().Err(err).Msg("WebSocket read error in log sink")
			}
			break
		}

		ls.handleMessage(message)
	}
}

func (ls *LogSink) handleMessage(data []byte) {
	var msg LogMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Warn().Err(err).Str("data", string(data)).Msg("Failed to parse log message from site connector")
		return
	}

	if msg.Type != "log" {
		log.Debug().Str("type", msg.Type).Msg("Ignoring non-log message from site connector")
		return
	}

	if msg.SiteID == "" {
		log.Warn().Msg("Log message missing site_id")
		return
	}

	if msg.Level == "" {
		msg.Level = "info"
	}

	fields := msg.Metadata
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["site_id"] = msg.SiteID

	if msg.Timestamp > 0 {
		fields["timestamp"] = msg.Timestamp
	}

	lokiWriter := logging.GetLokiWriter()
	if lokiWriter != nil {
		if err := lokiWriter.SendRemoteLog(msg.SiteID, msg.Level, msg.Message, fields); err != nil {
			log.Error().Err(err).Str("site_id", msg.SiteID).Msg("Failed to send remote log to Loki")
		} else {
			log.Debug().
				Str("site_id", msg.SiteID).
				Str("level", msg.Level).
				Str("message", msg.Message).
				Msg("Remote log sent to Loki")
		}
	} else {
		log.Warn().Str("site_id", msg.SiteID).Msg("Loki writer not initialized, remote log dropped")
	}
}

func (ls *LogSink) Close() {
	for conn := range ls.clients {
		conn.Close()
	}
}
