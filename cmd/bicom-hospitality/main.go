package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/sagostin/pbx-hospitality/internal/api"
	"github.com/sagostin/pbx-hospitality/internal/config"
	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/pbx"
	"github.com/sagostin/pbx-hospitality/internal/tenant"
)

func main() {
	// Parse command-line flags
	healthCheck := flag.Bool("health-check", false, "Run health check and exit (for Docker HEALTHCHECK)")
	flag.Parse()

	// Configure zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	// Configure logging output
	logDir := os.Getenv("LOG_DIR")
	if logDir != "" {
		// Ensure log directory exists
		if err := os.MkdirAll(logDir, 0755); err != nil {
			log.Fatal().Err(err).Str("log_dir", logDir).Msg("Failed to create log directory")
		}
		// Open log file with rotation
		logFile := filepath.Join(logDir, "hospitality.log")
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal().Err(err).Str("log_file", logFile).Msg("Failed to open log file")
		}
		defer f.Close()
		// Write JSON logs to file; console writer to stderr
		log.Logger = zerolog.New(f).With().Timestamp().Logger()
	} else {
		// Default: console output to stderr
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	log.Info().Msg("Starting Bicom Hospitality PMS Integration")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load configuration")
	}

	// If running health check, validate and exit
	if *healthCheck {
		os.Exit(runHealthCheck(cfg))
	}

	// Initialize context for main operation
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
			if err := db.AutoMigrate(database); err != nil {
				log.Warn().Err(err).Msg("Auto-migration failed, continuing with existing schema")
			}
			defer database.Close()
		}
	} else {
		log.Warn().Msg("Database not configured, running without persistence")
	}

	// Initialize tenant manager
	tm, err := tenant.NewManager(database)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize tenant manager")
	}

	// Initialize PBX manager
	pbxMgr := pbx.NewManager(database)
	if database != nil {
		if err := pbxMgr.LoadFromDB(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to load PBX systems from database")
		}
	}

	// Load tenants from database
	if err := tm.LoadFromDB(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to load tenants from database")
	}

	// Start all tenants
	if err := tm.StartAll(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to start tenants")
	}

	// Initialize HTTP API
	var router http.Handler
	if database != nil {
		router = api.NewRouterWithDB(tm, pbxMgr, cfg, database)
	} else {
		router = api.NewRouter(tm, pbxMgr, cfg)
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

	// Stop PBX manager (close all connections)
	if pbxMgr != nil {
		pbxMgr.Close()
	}

	log.Info().Msg("Shutdown complete")
}

// runHealthCheck validates the service health for Docker HEALTHCHECK
// Returns exit code 0 (healthy) or 1 (degraded)
func runHealthCheck(cfg *config.Config) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Initialize database (optional)
	var database *db.DB
	var dbErr error
	if cfg.Database.Host != "" {
		database, dbErr = db.New(ctx, db.Config{
			Host:     cfg.Database.Host,
			Port:     cfg.Database.Port,
			User:     cfg.Database.User,
			Password: cfg.Database.Password,
			Database: cfg.Database.Database,
			SSLMode:  cfg.Database.SSLMode,
		})
		if dbErr != nil {
			log.Error().Err(dbErr).Msg("Health check: database connection failed")
			return 1
		}
		defer database.Close()

		// Validate database connectivity
		sqlDB, err := database.DB.DB()
		if err != nil || sqlDB.PingContext(ctx) != nil {
			log.Error().Err(err).Msg("Health check: database ping failed")
			return 1
		}
	}

	// Initialize tenant manager
	tm, err := tenant.NewManager(database)
	if err != nil {
		log.Error().Err(err).Msg("Health check: failed to initialize tenant manager")
		return 1
	}

	// Load tenants from DB
	if err := tm.LoadFromDB(ctx); err != nil {
		log.Error().Err(err).Msg("Health check: failed to load tenants from DB")
		return 1
	}

	// Attempt to start tenants (this validates connectivity)
	if err := tm.StartAll(ctx); err != nil {
		log.Error().Err(err).Msg("Health check: tenant startup failed")
		tm.StopAll()
		return 1
	}

	// Stop tenants after validation
	tm.StopAll()

	log.Info().Msg("Health check passed")
	return 0
}
