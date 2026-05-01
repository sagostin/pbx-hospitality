package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// Config holds database connection settings
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

// DSN returns the PostgreSQL connection string
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}

// DB wraps pgxpool for database operations
type DB struct {
	pool *pgxpool.Pool
}

// New creates a new database connection pool
func New(ctx context.Context, cfg Config) (*DB, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parsing db config: %w", err)
	}

	// Set pool configuration
	poolCfg.MaxConns = 10
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = 30 * time.Minute
	poolCfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	log.Info().Str("host", cfg.Host).Str("database", cfg.Database).Msg("Database connected")

	return &DB{pool: pool}, nil
}

// Close closes the database connection pool
func (db *DB) Close() {
	if db.pool != nil {
		db.pool.Close()
	}
}

// Pool returns the underlying connection pool
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// =============================================================================
// Tenant Repository
// =============================================================================

// Tenant represents a row in the tenants table
type Tenant struct {
	ID        string
	Name      string
	PMSConfig map[string]interface{}
	PBXConfig map[string]interface{}
	Settings  map[string]interface{}
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GetTenant retrieves a tenant by ID
func (db *DB) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	var t Tenant
	err := db.pool.QueryRow(ctx, `
		SELECT id, name, pms_config, pbx_config, settings, enabled, created_at, updated_at
		FROM tenants WHERE id = $1
	`, id).Scan(&t.ID, &t.Name, &t.PMSConfig, &t.PBXConfig, &t.Settings, &t.Enabled, &t.CreatedAt, &t.UpdatedAt)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying tenant: %w", err)
	}
	return &t, nil
}

// ListTenants returns all tenants
func (db *DB) ListTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, name, pms_config, pbx_config, settings, enabled, created_at, updated_at
		FROM tenants ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("querying tenants: %w", err)
	}
	defer rows.Close()

	var tenants []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.PMSConfig, &t.PBXConfig, &t.Settings, &t.Enabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning tenant: %w", err)
		}
		tenants = append(tenants, t)
	}
	return tenants, nil
}

// =============================================================================
// Room Mapping Repository
// =============================================================================

// RoomMapping represents a room-to-extension mapping
type RoomMapping struct {
	ID         int
	TenantID   string
	RoomNumber string
	Extension  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// GetRoomMapping retrieves a room mapping
func (db *DB) GetRoomMapping(ctx context.Context, tenantID, roomNumber string) (*RoomMapping, error) {
	var rm RoomMapping
	err := db.pool.QueryRow(ctx, `
		SELECT id, tenant_id, room_number, extension, created_at, updated_at
		FROM room_mappings WHERE tenant_id = $1 AND room_number = $2
	`, tenantID, roomNumber).Scan(&rm.ID, &rm.TenantID, &rm.RoomNumber, &rm.Extension, &rm.CreatedAt, &rm.UpdatedAt)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying room mapping: %w", err)
	}
	return &rm, nil
}

// ListRoomMappings returns all room mappings for a tenant
func (db *DB) ListRoomMappings(ctx context.Context, tenantID string) ([]RoomMapping, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, tenant_id, room_number, extension, created_at, updated_at
		FROM room_mappings WHERE tenant_id = $1 ORDER BY room_number
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying room mappings: %w", err)
	}
	defer rows.Close()

	var mappings []RoomMapping
	for rows.Next() {
		var rm RoomMapping
		if err := rows.Scan(&rm.ID, &rm.TenantID, &rm.RoomNumber, &rm.Extension, &rm.CreatedAt, &rm.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning room mapping: %w", err)
		}
		mappings = append(mappings, rm)
	}
	return mappings, nil
}

// UpsertRoomMapping creates or updates a room mapping
func (db *DB) UpsertRoomMapping(ctx context.Context, tenantID, roomNumber, extension string) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO room_mappings (tenant_id, room_number, extension)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, room_number) DO UPDATE SET
			extension = EXCLUDED.extension,
			updated_at = NOW()
	`, tenantID, roomNumber, extension)
	if err != nil {
		return fmt.Errorf("upserting room mapping: %w", err)
	}
	return nil
}

// =============================================================================
// Guest Session Repository
// =============================================================================

// GuestSession represents an active or past guest stay
type GuestSession struct {
	ID            int
	TenantID      string
	RoomNumber    string
	Extension     string
	GuestName     string
	ReservationID string
	CheckIn       time.Time
	CheckOut      *time.Time
	Metadata      map[string]interface{}
}

// CreateGuestSession creates a new guest session (check-in)
func (db *DB) CreateGuestSession(ctx context.Context, tenantID, roomNumber, extension, guestName, reservationID string, metadata map[string]interface{}) (int, error) {
	var id int
	err := db.pool.QueryRow(ctx, `
		INSERT INTO guest_sessions (tenant_id, room_number, extension, guest_name, reservation_id, check_in, metadata)
		VALUES ($1, $2, $3, $4, $5, NOW(), $6)
		RETURNING id
	`, tenantID, roomNumber, extension, guestName, reservationID, metadata).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("creating guest session: %w", err)
	}
	return id, nil
}

// EndGuestSession marks a guest session as checked out
func (db *DB) EndGuestSession(ctx context.Context, tenantID, roomNumber string) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE guest_sessions SET check_out = NOW()
		WHERE tenant_id = $1 AND room_number = $2 AND check_out IS NULL
	`, tenantID, roomNumber)
	if err != nil {
		return fmt.Errorf("ending guest session: %w", err)
	}
	return nil
}

// GetActiveSession returns the current active session for a room
func (db *DB) GetActiveSession(ctx context.Context, tenantID, roomNumber string) (*GuestSession, error) {
	var gs GuestSession
	err := db.pool.QueryRow(ctx, `
		SELECT id, tenant_id, room_number, extension, guest_name, reservation_id, check_in, check_out, metadata
		FROM guest_sessions
		WHERE tenant_id = $1 AND room_number = $2 AND check_out IS NULL
		ORDER BY check_in DESC LIMIT 1
	`, tenantID, roomNumber).Scan(
		&gs.ID, &gs.TenantID, &gs.RoomNumber, &gs.Extension, &gs.GuestName,
		&gs.ReservationID, &gs.CheckIn, &gs.CheckOut, &gs.Metadata,
	)

	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying active session: %w", err)
	}
	return &gs, nil
}

// ListActiveSessions returns all active sessions for a tenant
func (db *DB) ListActiveSessions(ctx context.Context, tenantID string) ([]GuestSession, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, tenant_id, room_number, extension, guest_name, reservation_id, check_in, check_out, metadata
		FROM guest_sessions
		WHERE tenant_id = $1 AND check_out IS NULL
		ORDER BY room_number, check_in DESC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying active sessions: %w", err)
	}
	defer rows.Close()

	var sessions []GuestSession
	for rows.Next() {
		var gs GuestSession
		if err := rows.Scan(&gs.ID, &gs.TenantID, &gs.RoomNumber, &gs.Extension, &gs.GuestName, &gs.ReservationID, &gs.CheckIn, &gs.CheckOut, &gs.Metadata); err != nil {
			return nil, fmt.Errorf("scanning active session: %w", err)
		}
		sessions = append(sessions, gs)
	}
	return sessions, nil
}

// =============================================================================
// PMS Event Log Repository
// =============================================================================

// PMSEvent represents a logged PMS event
type PMSEvent struct {
	ID         int64
	TenantID   string
	EventType  string
	RoomNumber string
	Extension  string
	RawData    []byte
	Processed  bool
	Error      string
	CreatedAt  time.Time
}

// LogPMSEvent records a PMS event for audit/debugging
func (db *DB) LogPMSEvent(ctx context.Context, tenantID, eventType, roomNumber, extension string, rawData []byte) (int64, error) {
	var id int64
	err := db.pool.QueryRow(ctx, `
		INSERT INTO pms_events (tenant_id, event_type, room_number, extension, raw_data, processed)
		VALUES ($1, $2, $3, $4, $5, FALSE)
		RETURNING id
	`, tenantID, eventType, roomNumber, extension, rawData).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("logging PMS event: %w", err)
	}
	return id, nil
}

// MarkEventProcessed marks an event as successfully processed
func (db *DB) MarkEventProcessed(ctx context.Context, eventID int64) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE pms_events SET processed = TRUE WHERE id = $1
	`, eventID)
	return err
}

// MarkEventFailed marks an event as failed with an error message
func (db *DB) MarkEventFailed(ctx context.Context, eventID int64, errMsg string) error {
	_, err := db.pool.Exec(ctx, `
		UPDATE pms_events SET processed = TRUE, error = $2 WHERE id = $1
	`, eventID, errMsg)
	return err
}

// GetRecentEvents returns recent PMS events for a tenant
func (db *DB) GetRecentEvents(ctx context.Context, tenantID string, limit int) ([]PMSEvent, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT id, tenant_id, event_type, room_number, extension, raw_data, processed, COALESCE(error, ''), created_at
		FROM pms_events WHERE tenant_id = $1
		ORDER BY created_at DESC LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying PMS events: %w", err)
	}
	defer rows.Close()

	var events []PMSEvent
	for rows.Next() {
		var e PMSEvent
		if err := rows.Scan(&e.ID, &e.TenantID, &e.EventType, &e.RoomNumber, &e.Extension, &e.RawData, &e.Processed, &e.Error, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning PMS event: %w", err)
		}
		events = append(events, e)
	}
	return events, nil
}
