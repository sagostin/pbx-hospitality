package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the complete application configuration
type Config struct {
	Server         ServerConfig         `yaml:"server"`
	Database       DatabaseConfig       `yaml:"database"`
	Tenants        []TenantConfig      `yaml:"tenants"`
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
	Port int `yaml:"port"`
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

// TenantConfig holds per-tenant settings
type TenantConfig struct {
	ID         string    `yaml:"id"`
	Name       string    `yaml:"name"`
	PMS        PMSConfig `yaml:"pms"`
	PBX        PBXConfig `yaml:"pbx"`
	RoomPrefix string    `yaml:"room_prefix"`
	// Timezone for wake-up calls and event timestamps (e.g., "America/New_York")
	Timezone string `yaml:"timezone"`
	// Region for geo-routing and compliance (e.g., "us-east", "eu-west")
	Region string `yaml:"region"`
	// Enabled allows disabling a tenant without removing config
	Enabled *bool `yaml:"enabled,omitempty"`
}

// PMSConfig holds PMS connection settings
type PMSConfig struct {
	Protocol string `yaml:"protocol"` // "mitel", "fias", or "tigertms"
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	// Optional serial settings for Mitel
	SerialPort string `yaml:"serial_port,omitempty"`
	BaudRate   int    `yaml:"baud_rate,omitempty"`
	// TigerTMS-specific settings
	// PathPrefix is the URL path prefix for this tenant's TigerTMS endpoints
	// e.g., "/tigertms/hotel-gamma"
	PathPrefix string `yaml:"path_prefix,omitempty"`
	// AuthToken is the bearer token for TigerTMS authentication
	AuthToken string `yaml:"auth_token,omitempty"`
}

// PBXConfig holds PBX provider settings
// The Type field determines which provider implementation is used.
type PBXConfig struct {
	// Type identifies the PBX backend: "bicom" (default), "zultys", "freeswitch", etc.
	Type string `yaml:"type"`

	// ==========================================================================
	// Bicom/Asterisk-specific settings
	// ==========================================================================

	// ARI settings (for Asterisk-based PBXs like Bicom)
	ARIURL   string `yaml:"ari_url"`
	ARIWSUrl string `yaml:"ari_ws_url"`
	ARIUser  string `yaml:"ari_user"`
	ARIPass  string `yaml:"ari_pass"`
	AppName  string `yaml:"app_name"`

	// Bicom REST API settings (for extension/voicemail management)
	APIURL   string `yaml:"api_url"`   // e.g., "https://pbx.example.com"
	APIKey   string `yaml:"api_key"`   // API key from Admin Settings
	TenantID string `yaml:"tenant_id"` // Server/tenant ID

	// ==========================================================================
	// Zultys-specific settings
	// ==========================================================================

	// Auth settings for session-based authentication
	AuthURL  string `yaml:"auth_url"` // Login endpoint, e.g., "/api/auth/login"
	Username string `yaml:"username"` // Username for session auth
	Password string `yaml:"password"` // Password for session auth

	// Webhook settings for inbound PBX events
	WebhookSecret string `yaml:"webhook_secret"` // Secret for validating inbound webhooks
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

	return &cfg, nil
}
