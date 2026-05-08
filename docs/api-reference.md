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
```

**Individual mapping:**
```json
{
  "room_number": "101",
  "extension": "1101"
}
```

**Range mapping (sequential rooms):**
```json
{
  "room_number": "101",
  "room_end": "105",
  "extension": "201",
  "extension_end": "205"
}
```
Room 101 → extension 201, 102 → 202, ..., 105 → 205.

**Pattern mapping (regex):**
```json
{
  "match_pattern": "10[0-5]\\d",
  "extension": "500"
}
```
Matches rooms 100-159. The extension is applied to all matching rooms.

**Response (201):**
```json
{
  "status": "created",
  "room_number": "101",
  "extension": "1101"
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
