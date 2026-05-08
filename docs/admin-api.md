# Admin API Reference

Administrative API for managing PBX systems, sites, and tenants. All endpoints require the `X-Admin-Key` header.

## Base URL

```
http://localhost:8080
```

## Authentication

All admin endpoints require the `X-Admin-Key` header:

```http
X-Admin-Key: your-admin-api-key
```

## Error Responses

```json
{
  "error": "tenant not found",
  "code": "NOT_FOUND"
}
```

| Status | Code | Description |
|--------|------|-------------|
| 400 | VALIDATION_ERROR | Invalid request body or parameters |
| 401 | UNAUTHORIZED | Missing or invalid X-Admin-Key |
| 404 | NOT_FOUND | Resource does not exist |
| 409 | ALREADY_EXISTS | Resource already exists |
| 500 | INTERNAL_ERROR | Server error |
| 503 | DB_NOT_CONFIGURED | Database not configured |

---

## Sites

Sites represent physical properties that can host multiple tenants and connect to PBX systems.

### List Sites

```http
GET /admin/sites
```

**Response:**
```json
[
  {
    "id": "hotel-alpha",
    "name": "Hotel Alpha",
    "settings": {},
    "enabled": true,
    "created_at": "2026-01-02T10:00:00Z",
    "updated_at": "2026-01-02T10:00:00Z"
  }
]
```

### Get Site

```http
GET /admin/sites/{id}
```

**Response:**
```json
{
  "id": "hotel-alpha",
  "name": "Hotel Alpha",
  "settings": {},
  "enabled": true,
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Create Site

```http
POST /admin/sites
Content-Type: application/json

{
  "id": "hotel-alpha",
  "name": "Hotel Alpha",
  "auth_code": "your-16-char-minimum-auth-code",
  "settings": {},
  "enabled": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Unique identifier (alphanumeric with dashes, max 64 chars) |
| `name` | string | Yes | Display name (max 255 chars) |
| `auth_code` | string | Yes | Site connector auth code (min 16 chars, hashed on storage) |
| `settings` | object | No | Additional settings |
| `enabled` | boolean | No | Default true |

**Response (201):**
```json
{
  "id": "hotel-alpha",
  "name": "Hotel Alpha",
  "settings": {},
  "enabled": true,
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Update Site

```http
PUT /admin/sites/{id}
Content-Type: application/json

{
  "name": "Hotel Alpha Updated",
  "auth_code": "new-16-char-minimum-code"
}
```

All fields optional. Only provided fields are updated.

### Delete Site

```http
DELETE /admin/sites/{id}
```

Returns `204 No Content` on success.

### Get Site Health

```http
GET /admin/sites/{id}/health
```

**Response:**
```json
{
  "site_id": "hotel-alpha",
  "health_status": "healthy",
  "systems": [
    {
      "id": "pbx-1",
      "name": "Main PBX",
      "health_status": "connected",
      "api_url": "https://pbx.example.com"
    }
  ]
}
```

### List Site Bicom Mappings

```http
GET /admin/sites/{id}/bicom
```

**Response:**
```json
[
  {
    "id": 1,
    "site_id": "hotel-alpha",
    "bicom_system_id": "pbx-1",
    "priority": 1,
    "failover_enabled": true
  }
]
```

### Add Site Bicom Mapping

```http
POST /admin/sites/{id}/bicom
Content-Type: application/json

{
  "bicom_system_id": "pbx-1",
  "priority": 1,
  "failover_enabled": true
}
```

### Remove Site Bicom Mapping

```http
DELETE /admin/sites/{id}/bicom/{bicomSystemId}
```

Returns `204 No Content` on success.

### List Site Bicom Systems

```http
GET /admin/sites/{id}/bicom-systems
```

Returns the actual BicomSystem objects associated with the site, not just the mappings.

**Response:**
```json
[
  {
    "id": "pbx-1",
    "name": "Main PBX",
    "api_url": "https://pbx.example.com",
    "ari_url": "http://pbx.example.com:8088/ari",
    "health_status": "connected",
    "enabled": true
  }
]
```

---

## Tenants

Tenants represent hotel/property instances with PMS and PBX configurations.

### List Tenants

```http
GET /admin/tenants
```

**Response:**
```json
[
  {
    "id": "hotel-alpha",
    "site_id": "hotel-alpha",
    "name": "Hotel Alpha",
    "pms_config": {"protocol": "mitel"},
    "pbx_config": {"type": "bicom"},
    "settings": {},
    "enabled": true,
    "created_at": "2026-01-02T10:00:00Z",
    "updated_at": "2026-01-02T10:00:00Z"
  }
]
```

### Get Tenant

```http
GET /admin/tenants/{id}
```

### Create Tenant

```http
POST /admin/tenants
Content-Type: application/json

{
  "id": "hotel-alpha",
  "site_id": "hotel-alpha",
  "name": "Hotel Alpha",
  "pms_config": {
    "protocol": "mitel",
    "host": "10.0.1.50",
    "port": 23
  },
  "pbx_config": {
    "type": "bicom",
    "api_url": "https://pbx.example.com",
    "api_key": "your-api-key"
  },
  "settings": {},
  "enabled": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Unique identifier (alphanumeric with dashes, max 64 chars) |
| `site_id` | string | No | Associated site ID |
| `name` | string | Yes | Display name (max 255 chars) |
| `pms_config` | object | No | PMS configuration |
| `pbx_config` | object | No | PBX configuration |
| `settings` | object | No | Additional settings |
| `enabled` | boolean | No | Default true |

**pms_config.protocol** values: `mitel`, `fias`, `tigertms`  
**pbx_config.type** values: `bicom`, `zultys`, `freeswitch`

### Update Tenant

```http
PUT /admin/tenants/{id}
Content-Type: application/json

{
  "name": "Updated Hotel Name",
  "enabled": false
}
```

All fields optional. Only provided fields are updated.

### Delete Tenant

```http
DELETE /admin/tenants/{id}
```

Returns `204 No Content` on success.

### Import Tenants

```http
POST /admin/tenants/import
Content-Type: application/json

{
  "tenants": [
    {
      "id": "hotel-alpha",
      "name": "Hotel Alpha",
      "pms_config": {"protocol": "mitel"},
      "pbx_config": {"type": "bicom"}
    },
    {
      "id": "hotel-beta",
      "name": "Hotel Beta",
      "pms_config": {"protocol": "fias"},
      "pbx_config": {"type": "zultys"}
    }
  ]
}
```

**Response:**
```json
{
  "created": 2,
  "errors": []
}
```

### List Tenant Rooms

```http
GET /admin/tenants/{id}/rooms
```

**Response:**
```json
[
  {
    "id": 1,
    "tenant_id": "hotel-alpha",
    "room_number": "101",
    "room_end": null,
    "extension": "1101",
    "extension_end": null,
    "match_pattern": null,
    "created_at": "2026-01-02T10:00:00Z",
    "updated_at": "2026-01-02T10:00:00Z"
  }
]
```

Ranges and patterns include their respective fields. For example, a range entry:
```json
{
  "id": 2,
  "tenant_id": "hotel-alpha",
  "room_number": "201",
  "room_end": "205",
  "extension": "301",
  "extension_end": "305",
  "match_pattern": null,
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Get Tenant Room

```http
GET /admin/tenants/{id}/rooms/{room}
```

**Response:**
```json
{
  "id": 1,
  "tenant_id": "hotel-alpha",
  "room_number": "101",
  "room_end": null,
  "extension": "1101",
  "extension_end": null,
  "match_pattern": null,
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Delete Tenant Room

```http
DELETE /admin/tenants/{id}/rooms/{room}
```

Returns `204 No Content` on success.

### List Tenant Sessions

```http
GET /admin/tenants/{id}/sessions?all=true
```

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `all` | boolean | false | Set `true` to include checked-out sessions |

**Response (active only):**
```json
[
  {
    "id": 1,
    "tenant_id": "hotel-alpha",
    "room_number": "101",
    "extension": "1101",
    "guest_name": "John Smith",
    "check_in": "2026-01-02T14:00:00Z",
    "check_out": null
  }
]
```

### Get Tenant Session

```http
GET /admin/tenants/{id}/sessions/{room}
```

**Response:**
```json
{
  "id": 1,
  "tenant_id": "hotel-alpha",
  "room_number": "101",
  "extension": "1101",
  "guest_name": "John Smith",
  "check_in": "2026-01-02T14:00:00Z",
  "check_out": null
}
```

### Delete Tenant Session

```http
DELETE /admin/tenants/{id}/sessions/{room}
```

Force-deletes a guest session. Returns `204 No Content` on success.

### List Tenant Events

```http
GET /admin/tenants/{id}/events?processed=false&limit=50&offset=0
```

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `processed` | boolean | not set | Filter by processed status |
| `limit` | int | 50 | Max events to return (max 500) |
| `offset` | int | 0 | Number of events to skip |

**Response:**
```json
[
  {
    "id": 123,
    "tenant_id": "hotel-alpha",
    "event_type": "CheckIn",
    "room_number": "101",
    "extension": "1101",
    "processed": true,
    "error": "",
    "created_at": "2026-01-02T14:00:00Z"
  }
]
```

### Delete Tenant Event

```http
DELETE /admin/tenants/{id}/events/{eventID}
```

Returns `204 No Content` on success.

### Retry Tenant Event

```http
POST /admin/tenants/{id}/events/{eventID}/retry
```

Resets a failed event for reprocessing. The event's `processed` flag is set to `false` and `error` is cleared.

**Response:**
```json
{
  "status": "reset",
  "event_id": 123,
  "tenant": "hotel-alpha"
}
```

### Get Tenant Health

```http
GET /admin/tenants/{id}/health
```

**Response:**
```json
{
  "tenant_id": "hotel-alpha",
  "name": "Hotel Alpha",
  "pms_connected": true,
  "pbx_connected": true,
  "enabled": true,
  "room_count": 150,
  "active_sessions": 42
}
```

---

## Bicom Systems

PBX system connections for Bicom PBXware.

### List Bicom Systems

```http
GET /admin/bicom-systems
```

**Response:**
```json
[
  {
    "id": "pbx-1",
    "name": "Main PBX",
    "api_url": "https://pbx.example.com",
    "tenant_id": "hotel-alpha",
    "ari_url": "http://pbx.example.com:8088/ari",
    "ari_user": "hospitality",
    "ari_app_name": "hospitality",
    "webhook_url": "",
    "health_status": "connected",
    "settings": {},
    "enabled": true,
    "created_at": "2026-01-02T10:00:00Z",
    "updated_at": "2026-01-02T10:00:00Z"
  }
]
```

### Get Bicom System

```http
GET /admin/bicom-systems/{id}
```

### Create Bicom System

```http
POST /admin/bicom-systems
Content-Type: application/json

{
  "id": "pbx-1",
  "name": "Main PBX",
  "api_url": "https://pbx.example.com",
  "api_key": "your-api-key",
  "tenant_id": "hotel-alpha",
  "ari_url": "http://pbx.example.com:8088/ari",
  "ari_user": "hospitality",
  "ari_pass": "your-ari-password",
  "ari_app_name": "hospitality",
  "settings": {},
  "enabled": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | Yes | Unique identifier |
| `name` | string | Yes | Display name |
| `api_url` | string | Yes | Bicom REST API URL |
| `api_key` | string | Yes | Bicom API key |
| `tenant_id` | string | No | Associated tenant ID |
| `ari_url` | string | No | Asterisk ARI URL |
| `ari_user` | string | No | ARI username |
| `ari_pass` | string | No | ARI password (stored encrypted) |
| `ari_app_name` | string | No | ARI application name |
| `webhook_url` | string | No | Webhook callback URL |
| `settings` | object | No | Additional settings |
| `enabled` | boolean | No | Default true |

### Update Bicom System

```http
PUT /admin/bicom-systems/{id}
Content-Type: application/json

{
  "name": "Updated PBX Name",
  "api_key": "new-api-key"
}
```

### Delete Bicom System

```http
DELETE /admin/bicom-systems/{id}
```

Returns `204 No Content` on success.

### Update ARI Secret

```http
PUT /admin/bicom-systems/{id}/ari-secret
Content-Type: application/json

{
  "ari_pass": "new-ari-password"
}
```

Updates the ARI password and triggers PBX reload.

**Response:**
```json
{
  "status": "updated",
  "system": "pbx-1"
}
```

---

## PBX Management

### List PBX Status

```http
GET /admin/pbx/status
```

**Response:**
```json
[
  {
    "system_id": "pbx-1",
    "state": "connected",
    "last_seen": "2026-01-02T10:00:00Z"
  }
]
```

### Reload All PBX Systems

```http
POST /admin/pbx/reload
```

Reloads all PBX configurations from the database.

**Response:**
```json
{
  "status": "reloading",
  "scope": "all"
}
```

### Reload Specific PBX System

```http
POST /admin/pbx/{id}/reload
```

**Response:**
```json
{
  "status": "reloading",
  "system": "pbx-1"
}
```

---

## Setup Sequence

Proper setup order for a new installation:

1. **Create Site** - Foundation for tenant grouping
   ```bash
   curl -X POST http://localhost:8080/admin/sites \
     -H "X-Admin-Key: your-key" \
     -H "Content-Type: application/json" \
     -d '{"id":"hotel-alpha","name":"Hotel Alpha","auth_code":"your-secure-auth-code"}'
   ```

2. **Create Bicom System** - PBX connection details
   ```bash
   curl -X POST http://localhost:8080/admin/bicom-systems \
     -H "X-Admin-Key: your-key" \
     -H "Content-Type: application/json" \
     -d '{"id":"pbx-1","name":"Main PBX","api_url":"https://pbx.example.com","api_key":"your-api-key"}'
   ```

3. **Associate Site with Bicom System** - Link PBX to site
   ```bash
   curl -X POST http://localhost:8080/admin/sites/hotel-alpha/bicom \
     -H "X-Admin-Key: your-key" \
     -H "Content-Type: application/json" \
     -d '{"bicom_system_id":"pbx-1","priority":1,"failover_enabled":true}'
   ```

4. **Create Tenant** - Hotel/property instance
   ```bash
   curl -X POST http://localhost:8080/admin/tenants \
     -H "X-Admin-Key: your-key" \
     -H "Content-Type: application/json" \
     -d '{"id":"hotel-alpha","site_id":"hotel-alpha","name":"Hotel Alpha","pms_config":{"protocol":"mitel"},"pbx_config":{"type":"bicom"}}'
   ```

---

## Database Models

| Model | Table | Description |
|-------|-------|-------------|
| Site | `sites` | Physical property with auth code |
| Tenant | `tenants` | Hotel instance with PMS/PBX config |
| BicomSystem | `bicom_systems` | PBX connection configuration |
| SiteBicomMapping | `site_bicom_mappings` | Site-to-PBX associations |
| RoomMapping | `room_mappings` | Room number to extension mapping |
| GuestSession | `guest_sessions` | Guest check-in tracking |
| PMSEvent | `pms_events` | PMS event audit log |

Schema is created automatically via GORM AutoMigrate on startup.
