package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/topsoffice/bicom-hospitality/internal/api"
	"github.com/topsoffice/bicom-hospitality/internal/config"
	"github.com/topsoffice/bicom-hospitality/internal/db"
	"github.com/topsoffice/bicom-hospitality/internal/tenant"
)

func main() {
	// Configure zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	log.Info().Msg("Starting Bicom Hospitality PMS Integration")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize database (optional - continues without if not configured)
	var database *db.DB
	if cfg.Database.Host != "" {
		database, err = db.New(ctx, db.Config{
			Host:     cfg.Database.Host,
			Port:     cfg.Database.Port,
			User:     cfg.Database.User,
			Password: cfg.Database.Password,
			Database: cfg.Database.Database,
			SSLMode:  cfg.Database.SSLMode,
		})
		if err != nil {
			log.Warn().Err(err).Msg("Database connection failed, running without persistence")
		} else {
			defer database.Close()
		}
	} else {
		log.Warn().Msg("Database not configured, running without persistence")
	}

	// Initialize tenant manager
	tm, err := tenant.NewManager(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize tenant manager")
	}

	// Start all tenants
	if err := tm.StartAll(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to start tenants")
	}

	// Initialize HTTP API
	var router http.Handler
	if database != nil {
		router = api.NewRouterWithDB(tm, cfg, database)
	} else {
		router = api.NewRouter(tm, cfg)
	}
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		log.Info().Int("port", cfg.Server.Port).Msg("Starting HTTP server")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP server failed")
		}
	}()

	// Start config reload handler (SIGHUP)
	go handleConfigReload(tm)

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("Shutting down...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop HTTP server
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("HTTP server shutdown error")
	}

	// Stop all tenants
	tm.StopAll()

	log.Info().Msg("Shutdown complete")
}

// handleConfigReload listens for SIGHUP and reloads configuration
func handleConfigReload(tm *tenant.Manager) {
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)

	for {
		<-sighup
		log.Info().Msg("Received SIGHUP, reloading configuration...")

		newCfg, err := config.Load()
		if err != nil {
			log.Error().Err(err).Msg("Failed to reload configuration")
			continue
		}

		// Reload tenant configurations
		if err := tm.Reload(newCfg); err != nil {
			log.Error().Err(err).Msg("Failed to reload tenants")
			continue
		}

		log.Info().Int("tenants", len(newCfg.Tenants)).Msg("Configuration reloaded successfully")
	}
}
