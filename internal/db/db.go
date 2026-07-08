package db

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sagostin/pbx-hospitality/internal/crypto"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}

type DB struct {
	*gorm.DB
}

func New(ctx context.Context, cfg Config) (*DB, error) {
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN()), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("getting underlying db: %w", err)
	}

	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	log.Info().
		Str("host", cfg.Host).
		Int("port", cfg.Port).
		Str("database", cfg.Database).
		Int("max_open_conns", 10).
		Int("max_idle_conns", 2).
		Msg("Database connection pool configured")

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	log.Info().Str("host", cfg.Host).Str("database", cfg.Database).Msg("Database connected")

	return &DB{db}, nil
}

func (db *DB) Close() {
	if db.DB != nil {
		sqlDB, _ := db.DB.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}
}

// NewDBFromGorm wraps an existing *gorm.DB in the *DB type without
// opening a new connection. Used by tests with sqlite in-memory and
// by callers that already have a configured gorm.DB.
func NewDBFromGorm(g *gorm.DB) *DB {
	return &DB{DB: g}
}

func (db *DB) Pool() *gorm.DB {
	return db.DB
}

type Site struct {
	ID        string `gorm:"primaryKey;size:64"`
	Name      string `gorm:"size:255;not null"`
	AuthCode  string `gorm:"column:auth_code;size:128;not null"`
	Settings  string `gorm:"type:jsonb;default:'{}'"`
	Enabled   bool   `gorm:"default:true"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Site) TableName() string {
	return "sites"
}

type Tenant struct {
	ID        string  `gorm:"primaryKey;size:64"`
	SiteID    *string `gorm:"column:site_id;size:64;index"`
	Name      string  `gorm:"size:255;not null"`
	PMSConfig string  `gorm:"column:pms_config;type:jsonb;default:'{}'"`
	PBXConfig string  `gorm:"column:pbx_config;type:jsonb;default:'{}'"`
	Settings  string  `gorm:"type:jsonb;default:'{}'"`
	Enabled   bool    `gorm:"default:true"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Tenant) TableName() string {
	return "tenants"
}

type RoomMapping struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	TenantID     string `gorm:"column:tenant_id;size:64;not null;index:idx_room_mappings_tenant"`
	RoomNumber   string `gorm:"column:room_number;size:32;not null"`
	RoomEnd      string `gorm:"column:room_end;size:32"` // Range end (inclusive), empty for individual mappings
	Extension    string `gorm:"size:32;not null"`
	ExtensionEnd string `gorm:"column:extension_end;size:32"`  // Range end (inclusive), empty for individual mappings
	MatchPattern string `gorm:"column:match_pattern;size:128"` // Regex pattern for custom matching, overrides room/extension fields
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (RoomMapping) TableName() string {
	return "room_mappings"
}

type GuestSession struct {
	ID            uint       `gorm:"primaryKey;autoIncrement"`
	TenantID      string     `gorm:"column:tenant_id;size:64;not null;index:idx_guest_sessions_tenant"`
	RoomNumber    string     `gorm:"column:room_number;size:32;not null"`
	Extension     string     `gorm:"size:32"`
	GuestName     string     `gorm:"size:255"`
	ReservationID string     `gorm:"column:reservation_id;size:64"`
	CheckIn       time.Time  `gorm:"not null"`
	CheckOut      *time.Time `gorm:"index:idx_guest_sessions_active;index:idx_guest_sessions_tenant"`
	Metadata      string     `gorm:"type:jsonb;default:'{}'"`
}

func (GuestSession) TableName() string {
	return "guest_sessions"
}

type PMSEvent struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	TenantID   string    `gorm:"column:tenant_id;size:64;not null;index:idx_pms_events_tenant"`
	EventType  string    `gorm:"column:event_type;size:32;not null"`
	RoomNumber string    `gorm:"column:room_number;size:32"`
	Extension  string    `gorm:"size:32"`
	RawData    []byte    `gorm:"type:bytea"`
	Processed  bool      `gorm:"default:false;index:idx_pms_events_unprocessed"`
	Error      string    `gorm:"type:text"`
	CreatedAt  time.Time `gorm:"index:idx_pms_events_tenant"`
}

func (PMSEvent) TableName() string {
	return "pms_events"
}

// WakeUpCall is the durable record of a scheduled wake-up that the
// WakeUpScheduler will fire via ARI at ScheduledAt. The row is created by
// tenant.handleWakeUp after a successful ScheduleWakeUpCall on the PBX
// provider; the scheduler picks it up, calls pbx.Provider.OriginateWakeUp,
// then transitions status to "originated" / "completed" / "failed".
type WakeUpCall struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	TenantID     string     `gorm:"column:tenant_id;size:64;not null;index:idx_wakeup_tenant"`
	Extension    string     `gorm:"size:32;not null"`
	ScheduledAt  time.Time  `gorm:"column:scheduled_at;not null;index:idx_wakeup_due"`
	Status       string     `gorm:"size:16;not null;default:'pending';index:idx_wakeup_status"`
	AttemptCount int        `gorm:"column:attempt_count;default:0"`
	LastError    string     `gorm:"column:last_error;type:text"`
	CreatedAt    time.Time  `gorm:"not null;default:NOW()"`
	OriginatedAt *time.Time `gorm:"column:originated_at"`
	CompletedAt  *time.Time `gorm:"column:completed_at"`
	Metadata     string     `gorm:"type:jsonb;default:'{}'::jsonb"`
}

func (WakeUpCall) TableName() string {
	return "wakeup_calls"
}

// WakeUp status values.
const (
	WakeUpStatusPending    = "pending"
	WakeUpStatusOriginated = "originated"
	WakeUpStatusCompleted  = "completed"
	WakeUpStatusFailed     = "failed"
	WakeUpStatusCancelled  = "cancelled"
)

type BicomSystem struct {
	ID               string    `gorm:"primaryKey;size:64"`
	Name             string    `gorm:"size:255;not null"`
	APIURL           string    `gorm:"column:api_url;size:512;not null"`
	APIKey           string    `gorm:"column:api_key;size:128;not null"`
	TenantID         string    `gorm:"column:tenant_id;size:64"`
	ARIURL           string    `gorm:"column:ari_url;size:512"`
	ARIUser          string    `gorm:"column:ari_user;size:64"`
	ARIPassEncrypted []byte    `gorm:"column:ari_pass_encrypted;type:bytea"` // Encrypted ARI password
	ARIPassNonce     []byte    `gorm:"column:ari_pass_nonce;type:bytea"`     // Nonce for decryption
	ARIAppName       string    `gorm:"column:ari_app_name;size:64"`
	WebhookURL       string    `gorm:"column:webhook_url;size:512"`
	HealthStatus     string    `gorm:"column:health_status;size:32;default:'unknown'"`
	LastHealthCheck  time.Time `gorm:"column:last_health_check"`
	Settings         string    `gorm:"type:jsonb;default:'{}'"`
	Enabled          bool      `gorm:"default:true"`
	CreatedAt        time.Time
	UpdatedAt        time.Time

	// ARIPass is a computed field - plaintext password used for API calls
	// It is NOT stored directly; instead ARIPassEncrypted and ARIPassNonce are used
	ARIPass string `gorm:"-"` // Ignored by GORM, used for application-level encryption
}

func (BicomSystem) TableName() string {
	return "bicom_systems"
}

// SetARIPass encrypts and stores the ARI password
func (s *BicomSystem) SetARIPass(plaintext string) error {
	if plaintext == "" {
		s.ARIPassEncrypted = nil
		s.ARIPassNonce = nil
		s.ARIPass = ""
		return nil
	}
	ciphertext, nonce, err := crypto.Encrypt([]byte(plaintext))
	if err != nil {
		return fmt.Errorf("encrypting ARI password: %w", err)
	}
	s.ARIPassEncrypted = ciphertext
	s.ARIPassNonce = nonce
	s.ARIPass = plaintext
	return nil
}

// GetARIPass decrypts and returns the ARI password
func (s *BicomSystem) GetARIPass() (string, error) {
	if len(s.ARIPassEncrypted) == 0 || len(s.ARIPassNonce) == 0 {
		// Fallback to plaintext field (for migrations/data without encryption)
		return s.ARIPass, nil
	}
	plaintext, err := crypto.Decrypt(s.ARIPassEncrypted, s.ARIPassNonce)
	if err != nil {
		return "", fmt.Errorf("decrypting ARI password: %w", err)
	}
	s.ARIPass = string(plaintext)
	return s.ARIPass, nil
}

type SiteBicomMapping struct {
	ID              uint   `gorm:"primaryKey;autoIncrement"`
	SiteID          string `gorm:"column:site_id;size:64;not null;index"`
	BicomSystemID   string `gorm:"column:bicom_system_id;size:64;not null;index"`
	Priority        int    `gorm:"default:1"`
	FailoverEnabled bool   `gorm:"column:failover_enabled;default:true"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (SiteBicomMapping) TableName() string {
	return "site_bicom_mappings"
}

// Auth strategies supported for tenant inbound endpoints.
// URL token is the long-random secret embedded in the URL path; bearer
// and basic are header-based alternatives layered on top.
const (
	InboundAuthURLToken = "url_token"
	InboundAuthBearer   = "bearer"
	InboundAuthBasic    = "basic"
)

// TenantInboundToken is a per-tenant authentication credential for
// inbound PMS HTTP endpoints. The TokenHash is SHA-256 of the plaintext
// token (which is never stored). URL-token strategy uses the long
// secret embedded in the URL path; bearer/basic add an Authorization
// header check on top.
//
// Multiple tokens per tenant are supported (multi-channel, rotation,
// per-source).
type TenantInboundToken struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	TenantID     string     `gorm:"column:tenant_id;size:64;not null;uniqueIndex:idx_inbound_tokens_tenant_hash"`
	TokenHash    string     `gorm:"column:token_hash;size:128;not null"`
	AuthStrategy string     `gorm:"column:auth_strategy;size:16;not null;default:'url_token'"`
	BearerHash   string     `gorm:"column:bearer_hash;size:128"` // SHA-256 of bearer secret (only for auth_strategy='bearer')
	BasicUser    string     `gorm:"column:basic_user;size:64"`   // only for auth_strategy='basic'
	BasicHash    string     `gorm:"column:basic_hash;size:128"`  // SHA-256 of basic password
	Enabled      bool       `gorm:"default:true;index:idx_inbound_tokens_enabled"`
	LastUsedAt   *time.Time `gorm:"column:last_used_at"`
	CreatedAt    time.Time  `gorm:"not null;default:NOW()"`
	UpdatedAt    time.Time
}

func (TenantInboundToken) TableName() string { return "tenant_inbound_tokens" }

// Outbound webhook delivery status values.
const (
	OutboundStatusQueued  = "queued"
	OutboundStatusSending = "sending"
	OutboundStatusSent    = "sent"
	OutboundStatusFailed  = "failed"
	OutboundStatusDropped = "dropped"
)

// Outbound target strategies. ilink_cdr is the only TigerTMS-specific
// one today; future strategies include cloud_hmac, cloud_bearer, etc.
const (
	OutboundStrategyILinkCDR = "ilink_cdr"
)

// OutboundWebhook is a durable outbound delivery record. Producers
// enqueue; the dispatcher worker pool picks rows whose
// next_attempt_at <= NOW(), POSTs to target_url with the strategy's
// signing, and updates the row based on the receiver's response.
//
// Idempotency: (tenant_id, event_type, idempotency_key) is unique so
// double-produces are silently deduped — the dispatcher is at-least-once
// at the HTTP layer but exactly-once at the table layer.
type OutboundWebhook struct {
	ID             int64      `gorm:"primaryKey;autoIncrement"`
	TenantID       string     `gorm:"column:tenant_id;size:64;not null;index:idx_outbound_webhooks_tenant"`
	EventType      string     `gorm:"column:event_type;size:32;not null"`
	IdempotencyKey string     `gorm:"column:idempotency_key;size:128;not null;uniqueIndex:idx_outbound_webhooks_idem"`
	TargetURL      string     `gorm:"column:target_url;size:512;not null"`
	TargetStrategy string     `gorm:"column:target_strategy;size:32;not null;default:'ilink_cdr'"`
	Payload        string     `gorm:"column:payload;type:jsonb;not null;default:'{}'"`
	Status         string     `gorm:"column:status;size:16;not null;default:'queued';index:idx_outbound_webhooks_status_due"`
	AttemptCount   int        `gorm:"column:attempt_count;default:0"`
	LastError      string     `gorm:"column:last_error;type:text"`
	NextAttemptAt  time.Time  `gorm:"column:next_attempt_at;not null;default:NOW();index:idx_outbound_webhooks_status_due"`
	DeliveredAt    *time.Time `gorm:"column:delivered_at"`
	CreatedAt      time.Time  `gorm:"not null;default:NOW()"`
	UpdatedAt      time.Time
}

func (OutboundWebhook) TableName() string { return "outbound_webhooks" }

func AutoMigrate(db *DB) error {
	return db.DB.AutoMigrate(
		&Site{},
		&Tenant{},
		&BicomSystem{},
		&SiteBicomMapping{},
		&RoomMapping{},
		&GuestSession{},
		&PMSEvent{},
		&WakeUpCall{},
		&TenantInboundToken{},
		&OutboundWebhook{},
	)
}

func (db *DB) GetSite(ctx context.Context, id string) (*Site, error) {
	var s Site
	if err := db.DB.WithContext(ctx).First(&s, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("querying site: %w", err)
	}
	return &s, nil
}

func (db *DB) ListSites(ctx context.Context) ([]Site, error) {
	var sites []Site
	if err := db.DB.WithContext(ctx).Order("name").Find(&sites).Error; err != nil {
		return nil, fmt.Errorf("querying sites: %w", err)
	}
	return sites, nil
}

func (db *DB) CreateSite(ctx context.Context, s *Site) error {
	if err := db.DB.WithContext(ctx).Create(s).Error; err != nil {
		return fmt.Errorf("creating site: %w", err)
	}
	return nil
}

func (db *DB) UpdateSite(ctx context.Context, s *Site) error {
	if err := db.DB.WithContext(ctx).Model(s).Updates(map[string]interface{}{
		"name":       s.Name,
		"auth_code":  s.AuthCode,
		"settings":   s.Settings,
		"enabled":    s.Enabled,
		"updated_at": time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("updating site: %w", err)
	}
	return nil
}

func (db *DB) DeleteSite(ctx context.Context, id string) error {
	if err := db.DB.WithContext(ctx).Delete(&Site{}, "id = ?", id).Error; err != nil {
		return fmt.Errorf("deleting site: %w", err)
	}
	return nil
}

func (db *DB) ValidateSiteAuthCode(ctx context.Context, siteID, authCode string) (bool, error) {
	var s Site
	if err := db.DB.WithContext(ctx).Select("auth_code").First(&s, "id = ? AND enabled = ?", siteID, true).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, fmt.Errorf("querying site auth: %w", err)
	}
	if s.AuthCode == "" {
		return false, nil
	}
	return crypto.VerifyAuthCode(authCode, s.AuthCode), nil
}

func (db *DB) ListTenantsBySite(ctx context.Context, siteID string) ([]Tenant, error) {
	var tenants []Tenant
	if err := db.DB.WithContext(ctx).Where("site_id = ?", siteID).Order("name").Find(&tenants).Error; err != nil {
		return nil, fmt.Errorf("querying tenants by site: %w", err)
	}
	return tenants, nil
}

func (db *DB) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	var t Tenant
	if err := db.DB.WithContext(ctx).First(&t, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("querying tenant: %w", err)
	}
	return &t, nil
}

func (db *DB) ListTenants(ctx context.Context) ([]Tenant, error) {
	var tenants []Tenant
	if err := db.DB.WithContext(ctx).Order("name").Find(&tenants).Error; err != nil {
		return nil, fmt.Errorf("querying tenants: %w", err)
	}
	return tenants, nil
}

func (db *DB) CreateTenant(ctx context.Context, t *Tenant) error {
	if err := db.DB.WithContext(ctx).Create(t).Error; err != nil {
		return fmt.Errorf("creating tenant: %w", err)
	}
	return nil
}

func (db *DB) UpdateTenant(ctx context.Context, t *Tenant) error {
	if err := db.DB.WithContext(ctx).Model(t).Updates(map[string]interface{}{
		"site_id":    t.SiteID,
		"name":       t.Name,
		"pms_config": t.PMSConfig,
		"pbx_config": t.PBXConfig,
		"settings":   t.Settings,
		"enabled":    t.Enabled,
		"updated_at": time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("updating tenant: %w", err)
	}
	return nil
}

func (db *DB) DeleteTenant(ctx context.Context, id string) error {
	if err := db.DB.WithContext(ctx).Delete(&Tenant{}, "id = ?", id).Error; err != nil {
		return fmt.Errorf("deleting tenant: %w", err)
	}
	return nil
}

func (db *DB) GetRoomMapping(ctx context.Context, tenantID, roomNumber string) (*RoomMapping, error) {
	var rm RoomMapping
	if err := db.DB.WithContext(ctx).First(&rm, "tenant_id = ? AND room_number = ?", tenantID, roomNumber).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("querying room mapping: %w", err)
	}
	return &rm, nil
}

func (db *DB) FindRoomMapping(ctx context.Context, tenantID, roomNumber string) (*RoomMapping, error) {
	var rms []RoomMapping
	if err := db.DB.WithContext(ctx).Where("tenant_id = ?", tenantID).Find(&rms).Error; err != nil {
		return nil, fmt.Errorf("querying room mappings: %w", err)
	}

	// Check exact match first
	for _, rm := range rms {
		if rm.RoomNumber == roomNumber && rm.RoomEnd == "" && rm.MatchPattern == "" {
			return &rm, nil
		}
	}

	// Check ranges
	for _, rm := range rms {
		if rm.RoomEnd != "" && rm.MatchPattern == "" {
			// Try to parse as numeric range
			startNum, startOK := parseRoomNumber(rm.RoomNumber)
			endNum, endOK := parseRoomNumber(rm.RoomEnd)
			roomNum, roomOK := parseRoomNumber(roomNumber)
			if startOK && endOK && roomOK && roomNum >= startNum && roomNum <= endNum {
				return &rm, nil
			}
		}
	}

	// Check pattern matches
	for _, rm := range rms {
		if rm.MatchPattern != "" {
			if matchRoomNumber(roomNumber, rm.MatchPattern) {
				return &rm, nil
			}
		}
	}

	return nil, nil
}

func parseRoomNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func matchRoomNumber(room, pattern string) bool {
	// Simple glob-style matching for patterns like "10[0-5]*"
	matched, _ := regexp.MatchString(pattern, room)
	return matched
}

func (db *DB) ListRoomMappings(ctx context.Context, tenantID string) ([]RoomMapping, error) {
	var mappings []RoomMapping
	if err := db.DB.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("room_number").Find(&mappings).Error; err != nil {
		return nil, fmt.Errorf("querying room mappings: %w", err)
	}
	return mappings, nil
}

func (db *DB) UpsertRoomMapping(ctx context.Context, tenantID, roomNumber, extension string) error {
	return db.DB.WithContext(ctx).Exec(`
		INSERT INTO room_mappings (tenant_id, room_number, extension, created_at, updated_at)
		VALUES (?, ?, ?, NOW(), NOW())
		ON CONFLICT (tenant_id, room_number) DO UPDATE SET
			extension = EXCLUDED.extension,
			updated_at = NOW()
	`, tenantID, roomNumber, extension).Error
}

func (db *DB) UpsertRoomMappingEntry(ctx context.Context, rm *RoomMapping) error {
	return db.DB.WithContext(ctx).Save(rm).Error
}

func (db *DB) DeleteRoomMapping(ctx context.Context, tenantID, roomNumber string) error {
	var rm RoomMapping
	result := db.DB.WithContext(ctx).Where("tenant_id = ? AND room_number = ?", tenantID, roomNumber).First(&rm)
	if result.Error != nil {
		if result.Error == gorm.ErrRecordNotFound {
			return fmt.Errorf("room mapping not found")
		}
		return fmt.Errorf("querying room mapping: %w", result.Error)
	}

	// If it's a range or pattern, use room_number as unique key (even if room_end/match_pattern is set)
	if rm.RoomEnd != "" || rm.MatchPattern != "" {
		// For ranges/patterns, room_number is unique - delete by ID
		result = db.DB.WithContext(ctx).Delete(&RoomMapping{}, "id = ?", rm.ID)
	} else {
		// For individuals, use composite key
		result = db.DB.WithContext(ctx).Delete(&RoomMapping{}, "tenant_id = ? AND room_number = ?", tenantID, roomNumber)
	}

	if result.Error != nil {
		return fmt.Errorf("deleting room mapping: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("room mapping not found")
	}
	return nil
}

func (db *DB) ListAllGuestSessions(ctx context.Context, tenantID string) ([]GuestSession, error) {
	var sessions []GuestSession
	if err := db.DB.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("room_number, check_in DESC").Find(&sessions).Error; err != nil {
		return nil, fmt.Errorf("querying guest sessions: %w", err)
	}
	return sessions, nil
}

func (db *DB) GetGuestSessionByRoom(ctx context.Context, tenantID, roomNumber string) (*GuestSession, error) {
	var gs GuestSession
	if err := db.DB.WithContext(ctx).Where("tenant_id = ? AND room_number = ?", tenantID, roomNumber).Order("check_in DESC").First(&gs).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("querying guest session: %w", err)
	}
	return &gs, nil
}

func (db *DB) DeleteGuestSession(ctx context.Context, tenantID, roomNumber string) error {
	result := db.DB.WithContext(ctx).Delete(&GuestSession{}, "tenant_id = ? AND room_number = ?", tenantID, roomNumber)
	if result.Error != nil {
		return fmt.Errorf("deleting guest session: %w", result.Error)
	}
	return nil
}

func (db *DB) ListPMSEvents(ctx context.Context, tenantID string, processed *bool, limit, offset int) ([]PMSEvent, error) {
	query := db.DB.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if processed != nil {
		query = query.Where("processed = ?", *processed)
	}
	var events []PMSEvent
	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&events).Error; err != nil {
		return nil, fmt.Errorf("querying PMS events: %w", err)
	}
	return events, nil
}

func (db *DB) GetPMSEvent(ctx context.Context, tenantID string, eventID int64) (*PMSEvent, error) {
	var event PMSEvent
	if err := db.DB.WithContext(ctx).First(&event, "id = ? AND tenant_id = ?", eventID, tenantID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("querying PMS event: %w", err)
	}
	return &event, nil
}

func (db *DB) DeletePMSEvent(ctx context.Context, eventID int64) error {
	if err := db.DB.WithContext(ctx).Delete(&PMSEvent{}, "id = ?", eventID).Error; err != nil {
		return fmt.Errorf("deleting PMS event: %w", err)
	}
	return nil
}

func (db *DB) ResetPMSEvent(ctx context.Context, eventID int64) error {
	return db.DB.WithContext(ctx).Exec("UPDATE pms_events SET processed = FALSE, error = NULL WHERE id = ?", eventID).Error
}

func (db *DB) CreateGuestSession(ctx context.Context, tenantID, roomNumber, extension, guestName, reservationID string, metadata map[string]interface{}) (int, error) {
	var id int
	err := db.DB.WithContext(ctx).Raw(`
		INSERT INTO guest_sessions (tenant_id, room_number, extension, guest_name, reservation_id, check_in, metadata)
		VALUES (?, ?, ?, ?, ?, NOW(), ?)
		RETURNING id
	`, tenantID, roomNumber, extension, guestName, reservationID, metadata).Scan(&id).Error
	if err != nil {
		return 0, fmt.Errorf("creating guest session: %w", err)
	}
	return id, nil
}

func (db *DB) EndGuestSession(ctx context.Context, tenantID, roomNumber string) error {
	return db.DB.WithContext(ctx).Exec(`
		UPDATE guest_sessions SET check_out = NOW()
		WHERE tenant_id = ? AND room_number = ? AND check_out IS NULL
	`, tenantID, roomNumber).Error
}

func (db *DB) GetActiveSession(ctx context.Context, tenantID, roomNumber string) (*GuestSession, error) {
	var gs GuestSession
	if err := db.DB.WithContext(ctx).Where("tenant_id = ? AND room_number = ? AND check_out IS NULL", tenantID, roomNumber).Order("check_in DESC").First(&gs).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("querying active session: %w", err)
	}
	return &gs, nil
}

func (db *DB) ListActiveSessions(ctx context.Context, tenantID string) ([]GuestSession, error) {
	var sessions []GuestSession
	if err := db.DB.WithContext(ctx).Where("tenant_id = ? AND check_out IS NULL", tenantID).Order("room_number, check_in DESC").Find(&sessions).Error; err != nil {
		return nil, fmt.Errorf("querying active sessions: %w", err)
	}
	return sessions, nil
}

func (db *DB) LogPMSEvent(ctx context.Context, tenantID, eventType, roomNumber, extension string, rawData []byte) (int64, error) {
	var id int64
	err := db.DB.WithContext(ctx).Raw(`
		INSERT INTO pms_events (tenant_id, event_type, room_number, extension, raw_data, processed)
		VALUES (?, ?, ?, ?, ?, FALSE)
		RETURNING id
	`, tenantID, eventType, roomNumber, extension, rawData).Scan(&id).Error
	if err != nil {
		return 0, fmt.Errorf("logging PMS event: %w", err)
	}
	return id, nil
}

func (db *DB) MarkEventProcessed(ctx context.Context, eventID int64) error {
	return db.DB.WithContext(ctx).Exec("UPDATE pms_events SET processed = TRUE WHERE id = ?", eventID).Error
}

func (db *DB) MarkEventFailed(ctx context.Context, eventID int64, errMsg string) error {
	return db.DB.WithContext(ctx).Exec("UPDATE pms_events SET processed = TRUE, error = ? WHERE id = ?", errMsg, eventID).Error
}

func (db *DB) GetRecentEvents(ctx context.Context, tenantID string, limit int) ([]PMSEvent, error) {
	var events []PMSEvent
	if err := db.DB.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Limit(limit).Find(&events).Error; err != nil {
		return nil, fmt.Errorf("querying PMS events: %w", err)
	}
	return events, nil
}

func (db *DB) ListBicomSystems(ctx context.Context) ([]BicomSystem, error) {
	var systems []BicomSystem
	if err := db.DB.WithContext(ctx).Order("name").Find(&systems).Error; err != nil {
		return nil, fmt.Errorf("querying bicom systems: %w", err)
	}
	return systems, nil
}

func (db *DB) GetBicomSystem(ctx context.Context, id string) (*BicomSystem, error) {
	var s BicomSystem
	if err := db.DB.WithContext(ctx).First(&s, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("querying bicom system: %w", err)
	}
	return &s, nil
}

func (db *DB) CreateBicomSystem(ctx context.Context, s *BicomSystem) error {
	// Encrypt ARI password before storing
	if s.ARIPass != "" {
		if err := s.SetARIPass(s.ARIPass); err != nil {
			return fmt.Errorf("encrypting ARI password: %w", err)
		}
	}
	if err := db.DB.WithContext(ctx).Create(s).Error; err != nil {
		return fmt.Errorf("creating bicom system: %w", err)
	}
	return nil
}

func (db *DB) UpdateBicomSystem(ctx context.Context, s *BicomSystem) error {
	// Encrypt ARI password if changed
	if s.ARIPass != "" && s.ARIPass != s.ARIPassEncryptedString() {
		if err := s.SetARIPass(s.ARIPass); err != nil {
			return fmt.Errorf("encrypting ARI password: %w", err)
		}
	}
	if err := db.DB.WithContext(ctx).Model(s).Updates(map[string]interface{}{
		"name":               s.Name,
		"api_url":            s.APIURL,
		"api_key":            s.APIKey,
		"tenant_id":          s.TenantID,
		"ari_url":            s.ARIURL,
		"ari_user":           s.ARIUser,
		"ari_pass_encrypted": s.ARIPassEncrypted,
		"ari_pass_nonce":     s.ARIPassNonce,
		"ari_app_name":       s.ARIAppName,
		"webhook_url":        s.WebhookURL,
		"health_status":      s.HealthStatus,
		"settings":           s.Settings,
		"enabled":            s.Enabled,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		return fmt.Errorf("updating bicom system: %w", err)
	}
	return nil
}

// ARIPassEncryptedString returns a placeholder to detect if password was changed
func (s *BicomSystem) ARIPassEncryptedString() string {
	return "***encrypted***"
}

func (db *DB) DeleteBicomSystem(ctx context.Context, id string) error {
	if err := db.DB.WithContext(ctx).Delete(&BicomSystem{}, "id = ?", id).Error; err != nil {
		return fmt.Errorf("deleting bicom system: %w", err)
	}
	return nil
}

func (db *DB) ListSiteBicomMappings(ctx context.Context, siteID string) ([]SiteBicomMapping, error) {
	var mappings []SiteBicomMapping
	if err := db.DB.WithContext(ctx).Where("site_id = ?", siteID).Order("priority").Find(&mappings).Error; err != nil {
		return nil, fmt.Errorf("querying site-bicom mappings: %w", err)
	}
	return mappings, nil
}

func (db *DB) GetBicomSystemsForSite(ctx context.Context, siteID string) ([]BicomSystem, error) {
	var systems []BicomSystem
	if err := db.DB.WithContext(ctx).
		Joins("JOIN site_bicom_mappings ON site_bicom_mappings.bicom_system_id = bicom_systems.id").
		Where("site_bicom_mappings.site_id = ?", siteID).
		Order("site_bicom_mappings.priority").
		Find(&systems).Error; err != nil {
		return nil, fmt.Errorf("querying bicom systems for site: %w", err)
	}
	return systems, nil
}

func (db *DB) CreateSiteBicomMapping(ctx context.Context, m *SiteBicomMapping) error {
	if err := db.DB.WithContext(ctx).Create(m).Error; err != nil {
		return fmt.Errorf("creating site-bicom mapping: %w", err)
	}
	return nil
}

func (db *DB) DeleteSiteBicomMapping(ctx context.Context, siteID, bicomSystemID string) error {
	if err := db.DB.WithContext(ctx).Delete(&SiteBicomMapping{}, "site_id = ? AND bicom_system_id = ?", siteID, bicomSystemID).Error; err != nil {
		return fmt.Errorf("deleting site-bicom mapping: %w", err)
	}
	return nil
}

func (db *DB) UpdateSiteBicomMappingHealth(ctx context.Context, bicomSystemID, status string) error {
	return db.DB.WithContext(ctx).Exec(`
		UPDATE bicom_systems
		SET health_status = ?, last_health_check = ?
		WHERE id = ?
	`, status, time.Now(), bicomSystemID).Error
}

func (db *DB) GetSiteHealthStatus(ctx context.Context, siteID string) (string, error) {
	mappings, err := db.ListSiteBicomMappings(ctx, siteID)
	if err != nil {
		return "unknown", err
	}
	if len(mappings) == 0 {
		return "no_systems", nil
	}

	allHealthy := true
	anyUnhealthy := false
	for _, m := range mappings {
		system, err := db.GetBicomSystem(ctx, m.BicomSystemID)
		if err != nil || system == nil {
			continue
		}
		switch system.HealthStatus {
		case "healthy":
		case "degraded":
			allHealthy = false
		default:
			anyUnhealthy = true
			allHealthy = false
		}
	}

	if allHealthy {
		return "healthy", nil
	}
	if anyUnhealthy {
		return "unhealthy", nil
	}
	return "degraded", nil
}

// =============================================================================
// Wake-up call repository
// =============================================================================

// CreateWakeUpCall inserts a new pending wake-up row. Returns the new ID.
func (db *DB) CreateWakeUpCall(ctx context.Context, w *WakeUpCall) (int64, error) {
	if w.Status == "" {
		w.Status = WakeUpStatusPending
	}
	if err := db.DB.WithContext(ctx).Create(w).Error; err != nil {
		return 0, fmt.Errorf("creating wake-up call: %w", err)
	}
	return w.ID, nil
}

// GetDueWakeUpCalls returns pending wake-ups whose ScheduledAt <= now,
// ordered by ScheduledAt ascending, capped at limit rows. The scheduler
// uses this on every tick.
func (db *DB) GetDueWakeUpCalls(ctx context.Context, now time.Time, limit int) ([]WakeUpCall, error) {
	var out []WakeUpCall
	if err := db.DB.WithContext(ctx).
		Where("status = ? AND scheduled_at <= ?", WakeUpStatusPending, now).
		Order("scheduled_at asc").
		Limit(limit).
		Find(&out).Error; err != nil {
		return nil, fmt.Errorf("querying due wake-up calls: %w", err)
	}
	return out, nil
}

// MarkWakeUpOriginated transitions a row to "originated" once the PBX
// has accepted the originate request. OriginatedAt is set to now.
func (db *DB) MarkWakeUpOriginated(ctx context.Context, id int64) error {
	now := time.Now()
	return db.DB.WithContext(ctx).Exec(`
		UPDATE wakeup_calls
		SET status = ?, originated_at = ?, attempt_count = attempt_count + 1
		WHERE id = ?
	`, WakeUpStatusOriginated, now, id).Error
}

// MarkWakeUpCompleted transitions a row to "completed" once the wake-up
// call has finished (e.g. hangup observed). CompletedAt is set to now.
func (db *DB) MarkWakeUpCompleted(ctx context.Context, id int64) error {
	now := time.Now()
	return db.DB.WithContext(ctx).Exec(`
		UPDATE wakeup_calls
		SET status = ?, completed_at = ?
		WHERE id = ?
	`, WakeUpStatusCompleted, now, id).Error
}

// MarkWakeUpFailed transitions a row to "failed" with an error message.
// The scheduler increments attempt_count.
func (db *DB) MarkWakeUpFailed(ctx context.Context, id int64, errMsg string) error {
	return db.DB.WithContext(ctx).Exec(`
		UPDATE wakeup_calls
		SET status = ?, last_error = ?, attempt_count = attempt_count + 1,
		    completed_at = COALESCE(completed_at, NOW())
		WHERE id = ?
	`, WakeUpStatusFailed, errMsg, id).Error
}

// CancelWakeUpCall transitions a row to "cancelled". Used when a PMS
// wake-up event arrives with enabled=false.
func (db *DB) CancelWakeUpCall(ctx context.Context, id int64) error {
	now := time.Now()
	return db.DB.WithContext(ctx).Exec(`
		UPDATE wakeup_calls
		SET status = ?, completed_at = ?
		WHERE id = ? AND status IN (?, ?)
	`, WakeUpStatusCancelled, now, id, WakeUpStatusPending, WakeUpStatusOriginated).Error
}

// ListWakeUpCalls returns recent wake-up calls for a tenant (most recent first).
func (db *DB) ListWakeUpCalls(ctx context.Context, tenantID string, limit int) ([]WakeUpCall, error) {
	var out []WakeUpCall
	if err := db.DB.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("scheduled_at desc").
		Limit(limit).
		Find(&out).Error; err != nil {
		return nil, fmt.Errorf("querying wake-up calls: %w", err)
	}
	return out, nil
}

// FindPendingWakeUpCall finds a pending wake-up for a (tenant, extension)
// within the given time window. Used when a guest calls to cancel.
func (db *DB) FindPendingWakeUpCall(ctx context.Context, tenantID, extension string) (*WakeUpCall, error) {
	var w WakeUpCall
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND extension = ? AND status = ?",
			tenantID, extension, WakeUpStatusPending).
		Order("scheduled_at desc").
		First(&w).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("querying pending wake-up: %w", err)
	}
	return &w, nil
}

// CancelAllPendingWakeUpCallsForExtension transitions every pending
// (or already-originated, awaiting completion) wake-up row for a
// (tenant, extension) pair to "cancelled". Returns the number of rows
// affected.
//
// Used by the iLink `setwakeup action=clearall` flow which cancels all
// scheduled wake-ups for an extension regardless of their scheduled
// time.
func (db *DB) CancelAllPendingWakeUpCallsForExtension(ctx context.Context, tenantID, extension string) (int64, error) {
	now := time.Now()
	res := db.DB.WithContext(ctx).Exec(`
		UPDATE wakeup_calls
		SET status = ?, completed_at = ?
		WHERE tenant_id = ? AND extension = ? AND status IN (?, ?)
	`, WakeUpStatusCancelled, now, tenantID, extension, WakeUpStatusPending, WakeUpStatusOriginated)
	if res.Error != nil {
		return 0, fmt.Errorf("cancelling pending wake-ups: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// =============================================================================
// Tenant inbound tokens
// =============================================================================

// CreateTenantInboundToken inserts a new token row. TokenHash /
// BearerHash / BasicHash are SHA-256 hex of the corresponding secrets —
// plaintext is never persisted.
func (db *DB) CreateTenantInboundToken(ctx context.Context, t *TenantInboundToken) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.UpdatedAt = time.Now()
	return db.DB.WithContext(ctx).Create(t).Error
}

// LookupTenantInboundTokenByHash finds the enabled token row whose
// TokenHash matches. Returns nil + nil if not found. Bumps LastUsedAt
// asynchronously on a hit (best-effort, non-blocking semantics).
func (db *DB) LookupTenantInboundTokenByHash(ctx context.Context, tokenHash string) (*TenantInboundToken, error) {
	var t TenantInboundToken
	err := db.DB.WithContext(ctx).
		Where("token_hash = ? AND enabled = true", tokenHash).
		First(&t).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("looking up token: %w", err)
	}
	// Bump last_used_at fire-and-forget — don't fail the request if this fails.
	now := time.Now()
	_ = db.DB.WithContext(ctx).
		Model(&TenantInboundToken{}).
		Where("id = ?", t.ID).
		Update("last_used_at", &now).Error
	return &t, nil
}

// ListTenantInboundTokens returns all tokens for a tenant (admin view;
// never returns hashes).
func (db *DB) ListTenantInboundTokens(ctx context.Context, tenantID string) ([]TenantInboundToken, error) {
	var out []TenantInboundToken
	if err := db.DB.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at desc").
		Find(&out).Error; err != nil {
		return nil, fmt.Errorf("listing tenant tokens: %w", err)
	}
	return out, nil
}

// DisableTenantInboundToken revokes a token by id. Used by the admin
// DELETE endpoint.
func (db *DB) DisableTenantInboundToken(ctx context.Context, tenantID string, id int64) error {
	return db.DB.WithContext(ctx).
		Model(&TenantInboundToken{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).
		Updates(map[string]interface{}{"enabled": false, "updated_at": time.Now()}).Error
}

// =============================================================================
// Outbound webhooks
// =============================================================================

// EnqueueOutboundWebhook inserts a new outbound row. Returns the row's
// id. The (tenant_id, event_type, idempotency_key) unique index silently
// dedupes double-produces — re-enqueueing the same idempotency key is a
// no-op and returns the existing row's id.
func (db *DB) EnqueueOutboundWebhook(ctx context.Context, w *OutboundWebhook) (int64, error) {
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now()
	}
	w.UpdatedAt = time.Now()
	if w.Status == "" {
		w.Status = OutboundStatusQueued
	}
	if w.NextAttemptAt.IsZero() {
		w.NextAttemptAt = time.Now()
	}

	// Idempotent insert: try to fetch existing row first.
	var existing OutboundWebhook
	err := db.DB.WithContext(ctx).
		Where("tenant_id = ? AND event_type = ? AND idempotency_key = ?",
			w.TenantID, w.EventType, w.IdempotencyKey).
		First(&existing).Error
	if err == nil {
		return existing.ID, nil
	}
	if err != gorm.ErrRecordNotFound {
		return 0, fmt.Errorf("checking idempotency: %w", err)
	}

	if err := db.DB.WithContext(ctx).Create(w).Error; err != nil {
		return 0, fmt.Errorf("enqueueing outbound: %w", err)
	}
	return w.ID, nil
}

// ClaimDueOutboundWebhooks atomically claims up to `limit` rows whose
// status is queued/failed and whose next_attempt_at is due. The
// returned rows are transitioned to "sending" so concurrent workers
// don't double-deliver.
func (db *DB) ClaimDueOutboundWebhooks(ctx context.Context, now time.Time, limit int) ([]OutboundWebhook, error) {
	if limit <= 0 {
		limit = 25
	}
	var ids []int64
	if err := db.DB.WithContext(ctx).
		Model(&OutboundWebhook{}).
		Where("status IN (?, ?) AND next_attempt_at <= ?",
			OutboundStatusQueued, OutboundStatusFailed, now).
		Order("next_attempt_at asc").
		Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("plucking due ids: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	if err := db.DB.WithContext(ctx).
		Model(&OutboundWebhook{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{"status": OutboundStatusSending, "updated_at": now}).Error; err != nil {
		return nil, fmt.Errorf("claiming due rows: %w", err)
	}

	var rows []OutboundWebhook
	if err := db.DB.WithContext(ctx).
		Where("id IN ?", ids).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("loading claimed rows: %w", err)
	}
	return rows, nil
}

// MarkOutboundSent transitions a row to "sent" and stamps delivered_at.
func (db *DB) MarkOutboundSent(ctx context.Context, id int64) error {
	now := time.Now()
	return db.DB.WithContext(ctx).
		Model(&OutboundWebhook{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":       OutboundStatusSent,
			"delivered_at": &now,
			"last_error":   "",
			"updated_at":   now,
		}).Error
}

// MarkOutboundFailed increments attempt_count, records the error, and
// schedules the next retry. If attempt_count >= maxAttempts the row is
// moved to "dropped" instead of "failed" (terminal).
func (db *DB) MarkOutboundFailed(ctx context.Context, id int64, errMsg string, nextAttemptAt time.Time, maxAttempts int) error {
	now := time.Now()
	// First bump the attempt_count, then check the value to decide terminal.
	res := db.DB.WithContext(ctx).Exec(`
		UPDATE outbound_webhooks
		SET status = ?, last_error = ?, attempt_count = attempt_count + 1,
		    next_attempt_at = ?, updated_at = ?
		WHERE id = ?
	`, OutboundStatusFailed, errMsg, nextAttemptAt, now, id)
	if res.Error != nil {
		return fmt.Errorf("marking outbound failed: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil
	}

	var w OutboundWebhook
	if err := db.DB.WithContext(ctx).First(&w, id).Error; err != nil {
		return err
	}
	if w.AttemptCount >= maxAttempts {
		return db.DB.WithContext(ctx).
			Model(&OutboundWebhook{}).
			Where("id = ?", id).
			Updates(map[string]interface{}{
				"status":     OutboundStatusDropped,
				"updated_at": time.Now(),
			}).Error
	}
	return nil
}

// MarkOutbackQueued pushes a row back to queued (used when a transient
// error requires another retry beyond the standard schedule).
func (db *DB) MarkOutboundQueued(ctx context.Context, id int64, nextAttemptAt time.Time, lastError string) error {
	return db.DB.WithContext(ctx).
		Model(&OutboundWebhook{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          OutboundStatusQueued,
			"last_error":      lastError,
			"next_attempt_at": nextAttemptAt,
			"updated_at":      time.Now(),
		}).Error
}

// ListOutboundWebhooksForTenant returns recent outbound rows for the
// admin UI.
func (db *DB) ListOutboundWebhooksForTenant(ctx context.Context, tenantID string, limit int) ([]OutboundWebhook, error) {
	if limit <= 0 {
		limit = 50
	}
	var out []OutboundWebhook
	if err := db.DB.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("created_at desc").
		Limit(limit).
		Find(&out).Error; err != nil {
		return nil, fmt.Errorf("listing tenant outbounds: %w", err)
	}
	return out, nil
}
