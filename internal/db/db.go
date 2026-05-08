package db

import (
	"context"
	"fmt"
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
		Logger: logger.Default.LogMode(logger.Silent),
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
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	TenantID   string `gorm:"column:tenant_id;size:64;not null;index:idx_room_mappings_tenant"`
	RoomNumber string `gorm:"column:room_number;size:32;not null"`
	Extension  string `gorm:"size:32;not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
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

func AutoMigrate(db *DB) error {
	return db.DB.AutoMigrate(
		&Site{},
		&Tenant{},
		&BicomSystem{},
		&SiteBicomMapping{},
		&RoomMapping{},
		&GuestSession{},
		&PMSEvent{},
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
