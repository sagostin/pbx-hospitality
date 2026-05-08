package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds the complete application configuration
type Config struct {
	Server         ServerConfig          `yaml:"server"`
	Database       DatabaseConfig        `yaml:"database"`
	Crypto         CryptoConfig          `yaml:"crypto"`
	Logging        LoggingConfig         `yaml:"logging"`
	SiteConnectors []SiteConnectorConfig `yaml:"site_connectors"`
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

// WebSocketLogConfig holds WebSocket log sink settings
type WebSocketLogConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Path      string `yaml:"path"`
	AuthToken string `yaml:"auth_token"`
}

// TenantConfig holds per-tenant configuration (loaded from DB)
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
	ARIPass       string `yaml:"aripass"`
	AppName       string `yaml:"app_name"`
	APIURL        string `yaml:"api_url"`
	APIKey        string `yaml:"api_key"`
	TenantID      string `yaml:"tenant_id"`
	AuthURL       string `yaml:"auth_url"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	WebhookSecret string `yaml:"webhook_secret"`
}

// TenantSettings holds feature flags and access codes
type TenantSettings struct {
	RoomPrefix     string         `yaml:"room_prefix"`
	ExtensionRange [2]int         `yaml:"extension_range"`
	Features       TenantFeatures `yaml:"features"`
	AccessCodes    AccessCodes    `yaml:"access_codes"`
}

// TenantFeatures specifies which features are enabled
type TenantFeatures struct {
	WakeUpCalls   bool `yaml:"wake_up_calls"`
	RoomCleanCode bool `yaml:"room_clean_code"`
	DND           bool `yaml:"dnd"`
	MWI           bool `yaml:"mwi"`
	Voicemail     bool `yaml:"voicemail"`
	CallForward   bool `yaml:"call_forward"`
}

// AccessCodes defines feature access codes
type AccessCodes struct {
	WakeUp       string `yaml:"wake_up"`
	RoomClean    string `yaml:"room_clean"`
	RoomService  string `yaml:"room_service"`
	DoNotDisturb string `yaml:"do_not_disturb"`
	Voicemail    string `yaml:"voicemail"`
}

// SiteConnectorConfig describes a standalone PMS listener
type SiteConnectorConfig struct {
	Protocol      string       `yaml:"protocol"`
	ListenHost    string       `yaml:"listen_host"`
	ListenPort    int          `yaml:"listen_port"`
	AllowedPMSIPs []string     `yaml:"allowed_pms_ips,omitempty"`
	Output        OutputConfig `yaml:"output"`
}

// OutputConfig holds output settings for site connectors
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

// DSN returns the PostgreSQL connection string
func (d *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Database, d.SSLMode,
	)
}

// Load reads configuration from environment variables only
func Load() (*Config, error) {
	cfg := &Config{}

	if err := loadEnvOverrides(cfg); err != nil {
		return nil, err
	}

	applyDefaults(cfg)

	return cfg, nil
}

func loadEnvOverrides(cfg *Config) error {
	if lokiEndpoint := os.Getenv("LOKI_ENDPOINT"); lokiEndpoint != "" {
		cfg.Logging.LokiURL = lokiEndpoint
	}
	if lokiEnabled := os.Getenv("LOKI_ENABLED"); lokiEnabled != "" {
		cfg.Logging.LokiEnabled = lokiEnabled == "true" || lokiEnabled == "1"
	}
	if serviceName := os.Getenv("SERVICE_NAME"); serviceName != "" {
		cfg.Logging.WebSocketLogs.AuthToken = serviceName
	}
	if dbHost := os.Getenv("DB_HOST"); dbHost != "" {
		cfg.Database.Host = dbHost
	}
	if dbPort := os.Getenv("DB_PORT"); dbPort != "" {
		port, err := strconv.Atoi(dbPort)
		if err != nil {
			return fmt.Errorf("invalid DB_PORT: %w", err)
		}
		cfg.Database.Port = port
	}
	if dbUser := os.Getenv("DB_USER"); dbUser != "" {
		cfg.Database.User = dbUser
	}
	if dbPass := os.Getenv("DB_PASSWORD"); dbPass != "" {
		cfg.Database.Password = dbPass
	}
	if dbName := os.Getenv("DB_NAME"); dbName != "" {
		cfg.Database.Database = dbName
	}
	if dbSSL := os.Getenv("DB_SSL_MODE"); dbSSL != "" {
		cfg.Database.SSLMode = dbSSL
	}
	if serverPort := os.Getenv("SERVER_PORT"); serverPort != "" {
		port, err := strconv.Atoi(serverPort)
		if err != nil {
			return fmt.Errorf("invalid SERVER_PORT: %w", err)
		}
		cfg.Server.Port = port
	}
	if adminAPIKey := os.Getenv("ADMIN_API_KEY"); adminAPIKey != "" {
		cfg.Server.AdminAPIKey = adminAPIKey
	}
	if masterKey := os.Getenv("ENCRYPTION_MASTER_KEY"); masterKey != "" {
		cfg.Crypto.MasterKey = masterKey
	}
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		cfg.Logging.Level = logLevel
	}
	if logFormat := os.Getenv("LOG_FORMAT"); logFormat != "" {
		cfg.Logging.Format = logFormat
	}

	return nil
}

func applyDefaults(cfg *Config) {
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
}
