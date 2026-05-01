// site-connector is a standalone binary that runs PMS listeners (site-connector mode)
// without any database or HTTP API dependencies. It listens for incoming PMS
// connections (e.g. FIAS or Mitel) and emits structured pms.Event structs to
// stdout for downstream processing by the main hospitality service.
//
// Configuration is via config.yaml under the site_connectors list:
//
//	site_connectors:
//	  - protocol: fias          # or "mitel"
//	    listen_host: ""         # empty = all interfaces
//	    listen_port: 5000
//	    allowed_pms_ips:         # optional; omit to allow all
//	      - 192.168.1.100
//
// Each protocol listed must have called pms.RegisterListener in its init().
// Currently registered: fias (internal/pms/listener/fias_server.go),
// mitel (internal/pms/listener/mitel_server.go).
//
// Usage:
//
//	./site-connector                      # uses config/config.yaml
//	CONFIG_PATH=/path/to/config.yaml ./site-connector
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/config"
	"github.com/sagostin/pbx-hospitality/internal/pms"
	// Import listener sub-packages to trigger their init() registrations.
	// Each listener package registers itself with pms.ListenerRegistry on import.
	_ "github.com/sagostin/pbx-hospitality/internal/pms/listener/fias"
	_ "github.com/sagostin/pbx-hospitality/internal/pms/listener/mitel"
)

func main() {
	// Parse command-line flags
	showVersion := flag.Bool("version", false, "Print version and exit")
	listProtocols := flag.Bool("list-protocols", false, "List available listener protocols and exit")
	flag.Parse()

	// Configure zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "2006-01-02T15:04:05Z07:00"})

	// If listing protocols, just show what's registered and exit
	if *listProtocols {
		fmt.Println("Available site-connector (listener) protocols:")
		for proto := range pms.ListenerRegistry {
			fmt.Printf("  - %s\n", proto)
		}
		os.Exit(0)
	}

	// If version requested
	if *showVersion {
		fmt.Println("site-connector")
		os.Exit(0)
	}

	log.Info().Msg("Starting site-connector")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	if len(cfg.SiteConnectors) == 0 {
		log.Fatal().Msg("No site_connectors defined in config; nothing to do")
	}

	// Validate that requested protocols are registered
	for _, sc := range cfg.SiteConnectors {
		if _, ok := pms.ListenerRegistry[sc.Protocol]; !ok {
			log.Fatal().
				Str("protocol", sc.Protocol).
				Msg("Protocol not registered; is the listener package imported?")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start all listeners
	listeners := make([]pms.Listener, 0, len(cfg.SiteConnectors))
	for _, sc := range cfg.SiteConnectors {
		events := make(chan pms.Event, 100)
		l, err := pms.NewListener(sc.Protocol, pms.ListenerConfig{
			ListenHost:    sc.ListenHost,
			ListenPort:    sc.ListenPort,
			AllowedPMSIPs: sc.AllowedPMSIPs,
		}, events)
		if err != nil {
			log.Fatal().
				Err(err).
				Str("protocol", sc.Protocol).
				Msg("Failed to create listener")
		}

		// Start listener in background
		go func(proto string, listener pms.Listener, evts <-chan pms.Event) {
			log.Info().
				Str("protocol", proto).
				Str("host", listener.Host()).
				Int("port", listener.Port()).
				Msg("Listener starting")
			if err := listener.Listen(ctx); err != nil && err != context.Canceled {
				log.Error().
					Err(err).
					Str("protocol", proto).
					Msg("Listener error")
			}
		}(sc.Protocol, l, events)

		// Also pump events to stdout
		go func(proto string, evts <-chan pms.Event) {
			for {
				select {
				case <-ctx.Done():
					return
				case evt, ok := <-evts:
					if !ok {
						return
					}
					emitJSON, _ := json.Marshal(map[string]any{
						"protocol": proto,
						"event": map[string]any{
							"type":       evt.Type.String(),
							"room":       evt.Room,
							"guest_name": evt.GuestName,
							"status":     evt.Status,
							"timestamp":  evt.Timestamp.Format("2006-01-02T15:04:05Z"),
						},
					})
					os.Stdout.Write(emitJSON)
					os.Stdout.Write([]byte("\n"))
				}
			}
		}(sc.Protocol, events)

		listeners = append(listeners, l)
	}

	log.Info().Int("count", len(listeners)).Msg("All listeners started")

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down...")

	// Stop all listeners
	for _, l := range listeners {
		if err := l.Close(); err != nil {
			log.Error().Err(err).Msg("Error closing listener")
		}
	}

	log.Info().Msg("Shutdown complete")
}
