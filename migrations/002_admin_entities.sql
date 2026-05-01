-- Admin API: Clients, Systems, Sites
-- Migration 002 adds admin entities for the management API

-- Clients table
CREATE TABLE clients (
    id              VARCHAR(64) PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    region          VARCHAR(64) NOT NULL,
    contact_email   VARCHAR(255) NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_clients_updated_at
    BEFORE UPDATE ON clients
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Systems table (belongs to a client)
CREATE TABLE systems (
    id              VARCHAR(64) PRIMARY KEY,
    client_id       VARCHAR(64) NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    pms_type        VARCHAR(32) NOT NULL,  -- e.g., 'tigertms', 'mitel', 'fias'
    host            VARCHAR(255),
    port            INTEGER,
    serial_port     VARCHAR(128),
    baud_rate       INTEGER,
    credentials_json JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_systems_updated_at
    BEFORE UPDATE ON systems
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Sites table (belongs to a system)
CREATE TABLE sites (
    id              VARCHAR(64) PRIMARY KEY,
    system_id       VARCHAR(64) NOT NULL REFERENCES systems(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    pbx_type        VARCHAR(32) NOT NULL,  -- e.g., 'zultys', 'bicom'
    ari_url         VARCHAR(512),
    ari_ws_url      VARCHAR(512),
    ari_user        VARCHAR(128),
    api_url         VARCHAR(512),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TRIGGER update_sites_updated_at
    BEFORE UPDATE ON sites
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Encrypted secrets for sites (api_key, webhook_secret)
-- Uses the same encrypted_secrets table structure as crypto/secrets.go
-- but keyed by site_id + key_name

-- Extensions table (belongs to a site)
CREATE TABLE extensions (
    id              SERIAL PRIMARY KEY,
    site_id         VARCHAR(64) NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    extension       VARCHAR(32) NOT NULL,
    room_number     VARCHAR(32),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(site_id, extension),
    UNIQUE(site_id, room_number)
);

CREATE TRIGGER update_extensions_updated_at
    BEFORE UPDATE ON extensions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Indexes for admin API queries
CREATE INDEX idx_systems_client ON systems(client_id);
CREATE INDEX idx_sites_system ON sites(system_id);
CREATE INDEX idx_extensions_site ON extensions(site_id);

-- Function to update updated_at column
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';
