-- =============================================================================
-- pbx-hospitality schema
-- =============================================================================
-- The service auto-migrates this on first start (GORM AutoMigrate).
-- This file is a reference for ops + a starting point for adding manual
-- migrations. It must be kept in sync with internal/db/db.go.
-- =============================================================================

-- Tenants are loaded at startup. Each tenant owns its PMS and PBX configs.
CREATE TABLE IF NOT EXISTS tenants (
    id          VARCHAR(64) PRIMARY KEY,
    site_id     VARCHAR(64),
    name        VARCHAR(255) NOT NULL,
    pms_config  JSONB NOT NULL DEFAULT '{}'::jsonb,
    pbx_config  JSONB NOT NULL DEFAULT '{}'::jsonb,
    settings    JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tenants_site_id ON tenants(site_id);

-- Sites group multiple tenants (typically one physical hotel).
CREATE TABLE IF NOT EXISTS sites (
    id          VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    auth_code   VARCHAR(128) NOT NULL,
    settings    JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Bicom systems are PBX-as-a-shared-resource; tenants can reference them via
-- site_bicom_mappings instead of inlining credentials in tenants.pbx_config.
CREATE TABLE IF NOT EXISTS bicom_systems (
    id                  VARCHAR(64) PRIMARY KEY,
    name                VARCHAR(255) NOT NULL,
    api_url             VARCHAR(512) NOT NULL,
    api_key             VARCHAR(128) NOT NULL,
    tenant_id           VARCHAR(64),
    ari_url             VARCHAR(512),
    ari_user            VARCHAR(64),
    ari_pass_encrypted  BYTEA,
    ari_pass_nonce      BYTEA,
    ari_app_name        VARCHAR(64),
    webhook_url         VARCHAR(512),
    health_status       VARCHAR(32) NOT NULL DEFAULT 'unknown',
    last_health_check   TIMESTAMPTZ,
    settings            JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS site_bicom_mappings (
    id               SERIAL PRIMARY KEY,
    site_id          VARCHAR(64) NOT NULL,
    bicom_system_id  VARCHAR(64) NOT NULL,
    priority         INTEGER NOT NULL DEFAULT 1,
    failover_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(site_id, bicom_system_id)
);
CREATE INDEX IF NOT EXISTS idx_site_bicom_site_id ON site_bicom_mappings(site_id);

-- Room-to-extension mapping: individual, range, or regex pattern.
CREATE TABLE IF NOT EXISTS room_mappings (
    id            SERIAL PRIMARY KEY,
    tenant_id     VARCHAR(64) NOT NULL,
    room_number   VARCHAR(32) NOT NULL,
    room_end      VARCHAR(32),
    extension     VARCHAR(32) NOT NULL,
    extension_end VARCHAR(32),
    match_pattern VARCHAR(128),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, room_number)
);
CREATE INDEX IF NOT EXISTS idx_room_mappings_tenant ON room_mappings(tenant_id);

-- Active + historical guest sessions.
CREATE TABLE IF NOT EXISTS guest_sessions (
    id              SERIAL PRIMARY KEY,
    tenant_id       VARCHAR(64) NOT NULL,
    room_number     VARCHAR(32) NOT NULL,
    extension       VARCHAR(32),
    guest_name      VARCHAR(255),
    reservation_id  VARCHAR(64),
    check_in        TIMESTAMPTZ NOT NULL,
    check_out       TIMESTAMPTZ,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS idx_guest_sessions_tenant ON guest_sessions(tenant_id);
-- Partial index for the "active session per room" lookup.
CREATE INDEX IF NOT EXISTS idx_guest_sessions_active
    ON guest_sessions(tenant_id, room_number)
    WHERE check_out IS NULL;

-- Audit log of every PMS event; admin retry queue backs onto this.
CREATE TABLE IF NOT EXISTS pms_events (
    id           BIGSERIAL PRIMARY KEY,
    tenant_id    VARCHAR(64) NOT NULL,
    event_type   VARCHAR(32) NOT NULL,
    room_number  VARCHAR(32),
    extension    VARCHAR(32),
    raw_data     BYTEA,
    processed    BOOLEAN NOT NULL DEFAULT FALSE,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_pms_events_tenant ON pms_events(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_pms_events_unprocessed
    ON pms_events(tenant_id, created_at)
    WHERE processed = FALSE;