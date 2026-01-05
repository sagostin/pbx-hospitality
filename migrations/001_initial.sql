-- Bicom Hospitality PMS Integration - Initial Schema
-- This migration creates the core tables for multi-tenant PMS integration

-- Tenant configurations
CREATE TABLE tenants (
    id          VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    pms_config  JSONB NOT NULL DEFAULT '{}',
    pbx_config  JSONB NOT NULL DEFAULT '{}',
    settings    JSONB DEFAULT '{}',
    enabled     BOOLEAN DEFAULT TRUE,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Room-to-extension mappings
CREATE TABLE room_mappings (
    id          SERIAL PRIMARY KEY,
    tenant_id   VARCHAR(64) NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    room_number VARCHAR(32) NOT NULL,
    extension   VARCHAR(32) NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, room_number),
    UNIQUE(tenant_id, extension)
);

-- Guest sessions for tracking check-in/out
CREATE TABLE guest_sessions (
    id          SERIAL PRIMARY KEY,
    tenant_id   VARCHAR(64) NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    room_number VARCHAR(32) NOT NULL,
    extension   VARCHAR(32),
    guest_name  VARCHAR(255),
    reservation_id VARCHAR(64),
    check_in    TIMESTAMPTZ NOT NULL,
    check_out   TIMESTAMPTZ,
    metadata    JSONB DEFAULT '{}'
);

-- Audit log of PMS events
CREATE TABLE pms_events (
    id          BIGSERIAL PRIMARY KEY,
    tenant_id   VARCHAR(64) NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_type  VARCHAR(32) NOT NULL,
    room_number VARCHAR(32),
    extension   VARCHAR(32),
    raw_data    BYTEA,
    processed   BOOLEAN DEFAULT FALSE,
    error       TEXT,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX idx_room_mappings_tenant ON room_mappings(tenant_id);
CREATE INDEX idx_guest_sessions_active ON guest_sessions(tenant_id, room_number) 
    WHERE check_out IS NULL;
CREATE INDEX idx_guest_sessions_tenant ON guest_sessions(tenant_id, check_in DESC);
CREATE INDEX idx_pms_events_unprocessed ON pms_events(tenant_id, created_at) 
    WHERE processed = FALSE;
CREATE INDEX idx_pms_events_tenant ON pms_events(tenant_id, created_at DESC);

-- Function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Triggers for updated_at
CREATE TRIGGER update_tenants_updated_at
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_room_mappings_updated_at
    BEFORE UPDATE ON room_mappings
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
