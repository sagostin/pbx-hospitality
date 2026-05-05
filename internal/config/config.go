package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the complete application configuration
type Config struct {
	Server         ServerConfig          `yaml:"server"`
	Database       DatabaseConfig        `yaml:"database"`
	Crypto         CryptoConfig          `yaml:"crypto"`
	Logging        LoggingConfig         `yaml:"logging"`
	SiteConnectors []SiteConnectorConfig `yaml:"site_connectors"`
}

// SiteConnectorConfig describes a standalone PMS listener to run.
// The site-connector binary (cmd/site-connector) starts only the listeners
// defined here — no DB, no HTTP API, no tenant logic.
// Each entry registers a protocol listener (e.g. "fias", "mitel") via the
// pms.ListenerRegistry, so the protocol must be one that has called
// pms.RegisterListener in its init() function.
type SiteConnectorConfig struct {
	// Protocol identifies the listener implementation (e.g. "fias", "mitel").
	Protocol string `yaml:"protocol"`
	// ListenHost is the address to bind (empty = all interfaces).
	ListenHost string `yaml:"listen_host"`
	// ListenPort is the TCP port to listen on.
	ListenPort int `yaml:"listen_port"`
	// AllowedPMSIPs is an optional IP allowlist; if non-empty, only
	// connections from these IPs will be accepted.
	AllowedPMSIPs []string `yaml:"allowed_pms_ips,omitempty"`
}

// ServerConfig holds HTTP server settings
type ServerConfig struct {
	Port        int    `yaml:"port"`
	AdminAPIKey string `yaml:"admin_api_key"`
}

// DatabaseConfig holds PostgreSQL connection settings
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	SSLMode  string `yaml:"ssl_mode"`
}

// CryptoConfig holds encryption settings
type CryptoConfig struct {
	MasterKey string `yaml:"master_key"`
}

// LoggingConfig holds logging settings
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// DSN returns the PostgreSQL connection string
func (d *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Database, d.SSLMode,
	)
}

// Load reads configuration from file and environment
func Load() (*Config, error) {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config/config.yaml"
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	// Expand environment variables in config
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Apply defaults
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 5432
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "disable"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}

	return &cfg, nil
}
