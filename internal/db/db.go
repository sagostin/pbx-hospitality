package db

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
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

func AutoMigrate(db *DB) error {
	return db.DB.AutoMigrate(
		&Site{},
		&Tenant{},
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
	return s.AuthCode == authCode, nil
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