-- Add sites table for grouping tenants by physical location
CREATE TABLE sites (
    id          VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    auth_code   VARCHAR(128) NOT NULL,  -- Secret for site connector authentication
    settings    JSONB DEFAULT '{}',
    enabled     BOOLEAN DEFAULT TRUE,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    updated_at  TIMESTAMPTZ DEFAULT NOW()
);

-- Add site_id foreign key to tenants table
ALTER TABLE tenants ADD COLUMN site_id VARCHAR(64) REFERENCES sites(id) ON DELETE SET NULL;

-- Create index for site lookups
CREATE INDEX idx_tenants_site ON tenants(site_id);

-- Trigger for sites updated_at
CREATE TRIGGER update_sites_updated_at
    BEFORE UPDATE ON sites
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();