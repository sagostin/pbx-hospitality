-- =============================================================================
-- pbx-hospitality seed data (development only)
-- =============================================================================
-- Insert a sample site + tenant so the service has something to talk to on a
-- fresh dev install. The admin API key in .env.example must match what you use
-- when calling /admin/*.
--
--   psql -h localhost -U hospitality -d hospitality -f migrations/002_seed.sql
-- =============================================================================

-- Sample site
INSERT INTO sites (id, name, auth_code, enabled)
VALUES (
    'hotel-alpha',
    'Hotel Alpha (sample)',
    'dev-only-not-for-production-replace-me',
    TRUE
)
ON CONFLICT (id) DO NOTHING;

-- Sample tenant talking to a local FIAS listener on port 5000 and a Bicom PBX
-- whose API is reachable at https://pbx.example.com.
INSERT INTO tenants (
    id,
    site_id,
    name,
    pms_config,
    pbx_config,
    settings,
    enabled
)
VALUES (
    'hotel-alpha',
    'hotel-alpha',
    'Hotel Alpha',
    jsonb_build_object(
        'protocol', 'fias',
        'host', '127.0.0.1',
        'port', 5000
    ),
    jsonb_build_object(
        'type', 'bicom',
        'api_url', 'https://pbx.example.com',
        'api_key', 'replace-with-real-key',
        'tenant_id', '',
        'ari_url', '',
        'ari_user', '',
        'ari_pass', '',
        'app_name', 'bicom-hospitality'
    ),
    jsonb_build_object(
        'features', jsonb_build_object(
            'wake_up_calls', TRUE,
            'room_clean_code', FALSE,
            'dnd', TRUE,
            'mwi', TRUE,
            'voicemail', TRUE,
            'call_forward', FALSE
        ),
        'room_prefix', '1'
    ),
    TRUE
)
ON CONFLICT (id) DO NOTHING;

-- A handful of sample room mappings: rooms 101..110 → extensions 1101..1110.
INSERT INTO room_mappings (tenant_id, room_number, room_end, extension, extension_end)
SELECT 'hotel-alpha', lp.room_start, lp.room_end, lp.ext_start, lp.ext_end
FROM (
    VALUES
        ('101', '110', '1101', '1110')
) AS lp(room_start, room_end, ext_start, ext_end)
WHERE NOT EXISTS (
    SELECT 1 FROM room_mappings
    WHERE tenant_id = 'hotel-alpha'
      AND room_number = lp.room_start
);

-- Sample Bicom PBX system (separate from tenant config; available for sites).
INSERT INTO bicom_systems (
    id, name, api_url, api_key, ari_url, ari_user, ari_app_name,
    health_status, enabled
)
VALUES (
    'pbx-alpha',
    'Bicom PBX (sample)',
    'https://pbx.example.com',
    'replace-with-real-key',
    'http://pbx.example.com:8088/ari',
    'hospitality',
    'bicom-hospitality',
    'unknown',
    TRUE
)
ON CONFLICT (id) DO NOTHING;