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
	Protocol      string       `yaml:"protocol"`
	ListenHost    string       `yaml:"listen_host"`
	ListenPort    int          `yaml:"listen_port"`
	AllowedPMSIPs []string     `yaml:"allowed_pms_ips,omitempty"`
	Output        OutputConfig `yaml:"output"`
}

type OutputConfig struct {
	URL                 string `yaml:"url"`
	UseWebsocket        bool   `yaml:"use_websocket"`
	BufferEnabled       bool   `yaml:"buffer_enabled"`
	BufferDir           string `yaml:"buffer_dir"`
	BufferMaxSizeMB     int64  `yaml:"buffer_max_size_mb"`
	BatchEnabled        bool   `yaml:"batch_enabled"`
	BatchSize           int    `yaml:"batch_size"`
	BatchTimeoutSeconds int    `yaml:"batch_timeout_seconds"`
	BackpressureEnabled bool   `yaml:"backpressure_enabled"`
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
	Level         string             `yaml:"level"`
	Format        string             `yaml:"format"`
	LokiEnabled   bool               `yaml:"loki_enabled"`
	LokiURL       string             `yaml:"loki_url"`
	LokiBatchSize int                `yaml:"loki_batch_size"`
	LokiBatchWait int                `yaml:"loki_batch_wait"`
	WebSocketLogs WebSocketLogConfig `yaml:"websocket_logs"`
}

// WebSocketLogConfig holds WebSocket log sink settings for receiving logs from site connectors
type WebSocketLogConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Path      string `yaml:"path"`
	AuthToken string `yaml:"auth_token"`
}

// PMSConfig holds PMS connection settings
type PMSConfig struct {
	Protocol   string `yaml:"protocol"`
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	AuthToken  string `yaml:"auth_token"`
	PathPrefix string `yaml:"path_prefix"`
}

// PBXConfig holds PBX connection settings
type PBXConfig struct {
	Type          string `yaml:"type"`
	ARIURL        string `yaml:"ari_url"`
	ARIWSUrl      string `yaml:"ari_ws_url"`
	ARIUser       string `yaml:"ari_user"`
	ARIPass       string `yaml:"ari_pass"`
	AppName       string `yaml:"app_name"`
	APIURL        string `yaml:"api_url"`
	APIKey        string `yaml:"api_key"`
	TenantID      string `yaml:"tenant_id"`
	AuthURL       string `yaml:"auth_url"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// TenantConfig holds per-tenant configuration
type TenantConfig struct {
	ID         string         `yaml:"id"`
	Name       string         `yaml:"name"`
	SiteID     string         `yaml:"site_id"`
	PMS        PMSConfig      `yaml:"pms"`
	PBX        PBXConfig      `yaml:"pbx"`
	RoomPrefix string         `yaml:"room_prefix"`
	Timezone   string         `yaml:"timezone"`
	Settings   TenantSettings `yaml:"settings"`
}

// TenantSettings holds feature flags and access codes for a tenant
type TenantSettings struct {
	RoomPrefix     string         `yaml:"room_prefix"`     // Prepended to room numbers for extension mapping
	ExtensionRange [2]int         `yaml:"extension_range"` // Min/max extension numbers
	Features       TenantFeatures `yaml:"features"`        // Enabled features
	AccessCodes    AccessCodes    `yaml:"access_codes"`    // Feature access codes
}

// TenantFeatures specifies which features are enabled for a tenant
type TenantFeatures struct {
	WakeUpCalls   bool `yaml:"wake_up_calls"`
	RoomCleanCode bool `yaml:"room_clean_code"` // Code to signal room needs cleaning
	DND           bool `yaml:"dnd"`
	MWI           bool `yaml:"mwi"`
	Voicemail     bool `yaml:"voicemail"`
	CallForward   bool `yaml:"call_forward"`
}

// AccessCodes defines feature access codes
type AccessCodes struct {
	WakeUp       string `yaml:"wake_up"`        // e.g., "*411"
	RoomClean    string `yaml:"room_clean"`     // e.g., "*60"
	RoomService  string `yaml:"room_service"`   // e.g., "*70"
	DoNotDisturb string `yaml:"do_not_disturb"` // e.g., "*78"
	Voicemail    string `yaml:"voicemail"`      // e.g., "*98"
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
	if cfg.Logging.LokiBatchSize == 0 {
		cfg.Logging.LokiBatchSize = 100
	}
	if cfg.Logging.LokiBatchWait == 0 {
		cfg.Logging.LokiBatchWait = 5
	}
	if cfg.Logging.WebSocketLogs.Path == "" {
		cfg.Logging.WebSocketLogs.Path = "/ws/logs"
	}

	// Override from environment variables
	if lokiEndpoint := os.Getenv("LOKI_ENDPOINT"); lokiEndpoint != "" {
		cfg.Logging.LokiURL = lokiEndpoint
	}
	if lokiEnabled := os.Getenv("LOKI_ENABLED"); lokiEnabled != "" {
		cfg.Logging.LokiEnabled = lokiEnabled == "true" || lokiEnabled == "1"
	}
	if serviceName := os.Getenv("SERVICE_NAME"); serviceName != "" {
		cfg.Logging.WebSocketLogs.AuthToken = serviceName
	}

	return &cfg, nil
}
