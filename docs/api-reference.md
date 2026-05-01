# Hospitality API Reference

## Base URL

```
http://localhost:8080
```

---

## Health & Monitoring

### Health Check

```http
GET /health
```

**Response:**
```json
{
  "status": "ok",
  "database": "connected"
}
```

### Prometheus Metrics

```http
GET /metrics
```

Returns Prometheus-format metrics including:
- `hospitality_pms_events_total{tenant,type}` - Events received
- `hospitality_pms_event_duration_seconds{tenant,type}` - Processing latency
- `hospitality_pms_connection_status{tenant,protocol}` - Connection status
- `hospitality_guest_checkins_total{tenant}` - Check-in count

---

## Tenants

### List Tenants

```http
GET /api/v1/tenants
```

**Response:**
```json
[
  {
    "id": "hotel-alpha",
    "name": "Hotel Alpha",
    "pms_connected": true,
    "pbx_connected": true
  }
]
```

### Get Tenant

```http
GET /api/v1/tenants/{id}
```

**Response:**
```json
{
  "id": "hotel-alpha",
  "name": "Hotel Alpha"
}
```

### Get Tenant Status

```http
GET /api/v1/tenants/{id}/status
```

**Response:**
```json
{
  "id": "hotel-alpha",
  "name": "Hotel Alpha",
  "pms_connected": true,
  "pbx_connected": true
}
```

---

## Room Mappings

### List Room Mappings

```http
GET /api/v1/tenants/{id}/rooms
```

**Response:**
```json
[
  {
    "id": 1,
    "tenant_id": "hotel-alpha",
    "room_number": "101",
    "extension": "1101",
    "created_at": "2026-01-02T10:00:00Z",
    "updated_at": "2026-01-02T10:00:00Z"
  }
]
```

### Create Room Mapping

```http
POST /api/v1/tenants/{id}/rooms
Content-Type: application/json

{
  "room_number": "102",
  "extension": "1102"
}
```

**Response (201):**
```json
{
  "status": "created",
  "room_number": "102",
  "extension": "1102"
}
```

---

## Guest Sessions

### List Active Sessions

```http
GET /api/v1/tenants/{id}/sessions
```

**Response:**
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

### Get Session by Room

```http
GET /api/v1/tenants/{id}/sessions/{room}
```

**Response:**
```json
{
  "id": 1,
  "tenant_id": "hotel-alpha",
  "room_number": "101",
  "extension": "1101",
  "guest_name": "John Smith",
  "check_in": "2026-01-02T14:00:00Z"
}
```

---

## PMS Events

### List Recent Events

```http
GET /api/v1/tenants/{id}/events?limit=50
```

**Query Parameters:**
| Parameter | Type | Default | Max | Description |
|-----------|------|---------|-----|-------------|
| `limit` | int | 50 | 500 | Number of events |

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

---

## Error Responses

All errors return JSON with HTTP status code:

```json
{
  "error": "tenant not found"
}
```

| Status | Description |
|--------|-------------|
| 400 | Bad Request - Invalid input |
| 404 | Not Found - Resource doesn't exist |
| 503 | Service Unavailable - Database not configured |
| 500 | Internal Server Error |

---

## PBX Webhooks

### Receive PBX Call Event

```http
POST /api/v1/pbx/webhook/{tenant}
Content-Type: application/json
X-Webhook-Signature: sha256=<hmac-signature>

{
  "event": "access_code",
  "extension": "1015",
  "caller_id": "5551234567",
  "access_code": "*411",
  "timestamp": "2026-01-05T14:00:00Z"
}
```

**Response (200):**
```json
{"status":"ok"}
```

**Supported Events:**
| Event | Description |
|-------|-------------|
| `access_code` | Guest dialed a feature code |
| `incoming` | Incoming call to extension |
| `voicemail_left` | Voicemail message left |
| `call_end` | Call ended |

See [PBX Providers Guide](pbx-providers.md) for provider-specific details.

---

## Admin API

The Admin API provides full CRUD operations for managing clients, systems, and sites. This replaces the config.yaml-based configuration.

**Base URL:** `http://localhost:8080/api/v1/admin`

### Authentication

Admin endpoints require an `X-Admin-Token` header:
```http
X-Admin-Token: <admin-token>
```

---

## Clients

### List Clients

```http
GET /api/v1/admin/clients?limit=20&offset=0
```

**Query Parameters:**
| Parameter | Type | Default | Max | Description |
|-----------|------|---------|-----|-------------|
| `limit` | int | 20 | 100 | Number of clients |
| `offset` | int | 0 | - | Offset for pagination |

**Response (200):**
```json
{
  "data": [
    {
      "id": "uuid",
      "name": "Hotel Alpha",
      "region": "us-west",
      "contact_email": "admin@hotelalpha.com",
      "created_at": "2026-01-02T10:00:00Z",
      "updated_at": "2026-01-02T10:00:00Z"
    }
  ],
  "total": 1,
  "limit": 20,
  "offset": 0
}
```

### Create Client

```http
POST /api/v1/admin/clients
Content-Type: application/json

{
  "name": "Hotel Alpha",
  "region": "us-west",
  "contact_email": "admin@hotelalpha.com"
}
```

**Response (201):**
```json
{
  "id": "uuid",
  "name": "Hotel Alpha",
  "region": "us-west",
  "contact_email": "admin@hotelalpha.com",
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Get Client

```http
GET /api/v1/admin/clients/{id}
```

**Response (200):**
```json
{
  "id": "uuid",
  "name": "Hotel Alpha",
  "region": "us-west",
  "contact_email": "admin@hotelalpha.com",
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Update Client

```http
PUT /api/v1/admin/clients/{id}
Content-Type: application/json

{
  "name": "Hotel Alpha Updated",
  "region": "us-east",
  "contact_email": "newadmin@hotelalpha.com"
}
```

### Delete Client

```http
DELETE /api/v1/admin/clients/{id}
```

Deletes client and cascades to all associated systems and sites.

**Response (204):** No content

---

## Systems

### List Systems for Client

```http
GET /api/v1/admin/clients/{client_id}/systems
```

**Response (200):**
```json
[
  {
    "id": "uuid",
    "client_id": "client-uuid",
    "name": "Main PMS",
    "pms_type": "tigertms",
    "host": "pms.hotelalpha.com",
    "port": 8080,
    "created_at": "2026-01-02T10:00:00Z",
    "updated_at": "2026-01-02T10:00:00Z"
  }
]
```

### Create System

```http
POST /api/v1/admin/clients/{client_id}/systems
Content-Type: application/json

{
  "name": "Main PMS",
  "pms_type": "tigertms",
  "host": "pms.hotelalpha.com",
  "port": 8080,
  "serial_port": "/dev/ttyUSB0",
  "baud_rate": 9600,
  "credentials_json": {}
}
```

**Valid `pms_type` values:** `tigertms`, `mitel`, `fias`

**Response (201):**
```json
{
  "id": "uuid",
  "client_id": "client-uuid",
  "name": "Main PMS",
  "pms_type": "tigertms",
  "host": "pms.hotelalpha.com",
  "port": 8080,
  "serial_port": "/dev/ttyUSB0",
  "baud_rate": 9600,
  "credentials_json": {},
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Get System

```http
GET /api/v1/admin/systems/{id}
```

### Update System

```http
PUT /api/v1/admin/systems/{id}
Content-Type: application/json

{
  "name": "Main PMS Updated",
  "pms_type": "mitel",
  "host": "new-pms.hotelalpha.com",
  "port": 9090,
  "serial_port": "/dev/ttyUSB1",
  "baud_rate": 115200,
  "credentials_json": {"key": "value"}
}
```

### Delete System

```http
DELETE /api/v1/admin/systems/{id}
```

Deletes system and cascades to all associated sites.

**Response (204):** No content

---

## Sites

### List Sites for System

```http
GET /api/v1/admin/systems/{system_id}/sites
```

**Response (200):**
```json
[
  {
    "id": "uuid",
    "system_id": "system-uuid",
    "name": "Main PBX",
    "pbx_type": "zultys",
    "ari_url": "http://pbx.hotelalpha.com:8088",
    "created_at": "2026-01-02T10:00:00Z",
    "updated_at": "2026-01-02T10:00:00Z"
  }
]
```

### Create Site

```http
POST /api/v1/admin/systems/{system_id}/sites
Content-Type: application/json

{
  "name": "Main PBX",
  "pbx_type": "zultys",
  "ari_url": "http://pbx.hotelalpha.com:8088",
  "ari_ws_url": "ws://pbx.hotelalpha.com:8088/ws",
  "ari_user": "admin",
  "api_url": "http://pbx.hotelalpha.com/api",
  "api_key": "secret-api-key",
  "webhook_secret": "secret-webhook-key"
}
```

**Valid `pbx_type` values:** `zultys`, `bicom`

**Note:** `api_key` and `webhook_secret` are encrypted before storage. They will be redacted in API responses.

**Response (201):**
```json
{
  "id": "uuid",
  "system_id": "system-uuid",
  "name": "Main PBX",
  "pbx_type": "zultys",
  "ari_url": "http://pbx.hotelalpha.com:8088",
  "ari_ws_url": "ws://pbx.hotelalpha.com:8088/ws",
  "ari_user": "admin",
  "api_url": "http://pbx.hotelalpha.com/api",
  "api_key": "***REDACTED***",
  "webhook_secret": "***REDACTED***",
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Get Site

```http
GET /api/v1/admin/sites/{id}
```

Returns site details with secrets redacted as `***REDACTED***`.

**Response (200):**
```json
{
  "id": "uuid",
  "system_id": "system-uuid",
  "name": "Main PBX",
  "pbx_type": "zultys",
  "ari_url": "http://pbx.hotelalpha.com:8088",
  "ari_ws_url": "ws://pbx.hotelalpha.com:8088/ws",
  "ari_user": "admin",
  "api_url": "http://pbx.hotelalpha.com/api",
  "api_key": "***REDACTED***",
  "webhook_secret": "***REDACTED***",
  "created_at": "2026-01-02T10:00:00Z",
  "updated_at": "2026-01-02T10:00:00Z"
}
```

### Update Site

```http
PUT /api/v1/admin/sites/{id}
Content-Type: application/json

{
  "name": "Main PBX Updated",
  "pbx_type": "bicom",
  "ari_url": "http://new-pbx.hotelalpha.com:8088",
  "ari_ws_url": "ws://new-pbx.hotelalpha.com:8088/ws",
  "ari_user": "newadmin",
  "api_url": "http://new-pbx.hotelalpha.com/api",
  "api_key": "new-secret-api-key",
  "webhook_secret": "new-secret-webhook-key"
}
```

### Delete Site

```http
DELETE /api/v1/admin/sites/{id}
```

Deletes site and cascades to all associated extensions. Secrets are also deleted.

**Response (204):** No content

### Reload Site

```http
POST /api/v1/admin/sites/{id}/reload
```

Triggers a site reload (SIGHUP equivalent).

**Response (200):**
```json
{
  "status": "reload_triggered",
  "site_id": "uuid"
}
```

---

## Error Responses

All errors return JSON with HTTP status code:

```json
{
  "error": "resource not found"
}
```

| Status | Description |
|--------|-------------|
| 400 | Bad Request - Invalid input |
| 404 | Not Found - Resource doesn't exist |
| 503 | Service Unavailable - Database not configured |
| 500 | Internal Server Error |
