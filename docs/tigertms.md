# TigerTMS iLink Integration Guide

This document describes integrating TigerTMS iLink middleware with the Bicom Hospitality PMS Integration service.

---

## Overview

**TigerTMS iLink** is hospitality middleware that acts as a universal translator between Property Management Systems (PMS) and various hotel technology systems including telephony (PBX), TV, Wi-Fi, and guest services.

### Key Characteristics

| Aspect | Details |
|--------|---------|
| **Vendor** | TigerTMS (https://www.tigertms.com) |
| **Product** | iLink Hospitality Middleware |
| **Transport** | HTTP REST API |
| **Direction** | TigerTMS → PBX (push model) |
| **Format** | Query parameters or JSON body |

### How It Differs from Other Protocols

| Protocol | Transport | Direction | Our Role |
|----------|-----------|-----------|----------|
| Mitel SX-200 | TCP Socket | PMS → Us | Socket listener |
| Oracle FIAS | TCP Socket | PMS → Us | Socket listener |
| **TigerTMS** | **HTTP REST** | **Middleware → Us** | **HTTP server** |

With TigerTMS, **we expose HTTP endpoints** that receive events from the middleware, rather than connecting to a PMS socket.

---

## Architecture

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────────┐
│   Hotel PMS     │     │   TigerTMS      │     │   Bicom Hospitality │
│ (Opera, Shiji)  │────▶│   iLink         │────▶│   Integration       │
│                 │     │   Middleware    │     │   (HTTP Endpoints)  │
└─────────────────┘     └─────────────────┘     └─────────────────────┘
                                                         │
                                                         ▼
                                                ┌─────────────────┐
                                                │   Bicom PBXware │
                                                │   (ARI + API)   │
                                                └─────────────────┘
```

TigerTMS iLink:
1. Connects to the hotel's PMS using the PMS-native protocol
2. Translates PMS events to a standardized REST API format
3. Posts events to our HTTP endpoints
4. Optionally receives callbacks for CDR posting

---

## REST API Endpoints

TigerTMS expects these endpoints to be available on the PBX integration server:

### Guest Check-In / Check-Out

```
POST /API/setguest
```

**Parameters:**

| Field | Type | Description |
|-------|------|-------------|
| `room` | string | Room number |
| `checkin` | boolean | `true` for check-in, `false` for check-out |
| `guest` | string | Guest name (on check-in) |
| `lang` | string | Guest language preference (optional) |

**Example Request:**
```
POST /API/setguest?room=2129&checkin=true&guest=Smith%2C+John
```

**Response:**
```json
{"success": true, "message": "Guest checked in"}
```

---

### Class of Service (COS)

```
POST /API/setcos
```

Controls calling privileges for the room extension.

**Parameters:**

| Field | Type | Description |
|-------|------|-------------|
| `room` | string | Room number |
| `cos` | integer | Class of service level (0-9) |

**COS Levels (typical):**
- `0` = No outbound calls
- `1` = Local calls only
- `2` = National calls
- `3` = International calls

**Example:**
```
POST /API/setcos?room=2129&cos=2
```

---

### Message Waiting Indicator (MWI)

```
POST /API/setmw
```

Controls the message waiting light on room phones.

**Parameters:**

| Field | Type | Description |
|-------|------|-------------|
| `room` | string | Room number |
| `mw` | boolean | `true` = lamp on, `false` = lamp off |

**Example:**
```
POST /API/setmw?room=2129&mw=true
```

---

### SIP Data Update

```
POST /API/setsipdata
```

Updates SIP extension configuration data.

**Parameters:**

| Field | Type | Description |
|-------|------|-------------|
| `room` | string | Room number |
| `callerid` | string | Caller ID to display |
| `name` | string | Extension display name |

**Example:**
```
POST /API/setsipdata?room=2129&name=Smith%2C+John&callerid=2129
```

---

### DDI/DID Assignment

```
POST /API/setddi
```

Assigns or clears a Direct Dial-In number for a room.

**Parameters:**

| Field | Type | Description |
|-------|------|-------------|
| `room` | string | Room number |
| `ddi` | string | DDI number to assign (empty to clear) |

**Example:**
```
POST /API/setddi?room=2129&ddi=+14165551234
```

---

### Do Not Disturb (DND)

```
POST /API/setdnd
```

**Parameters:**

| Field | Type | Description |
|-------|------|-------------|
| `room` | string | Room number |
| `dnd` | boolean | `true` = DND on, `false` = DND off |

**Example:**
```
POST /API/setdnd?room=2129&dnd=true
```

---

### Wake-Up Calls

```
POST /API/setwakeup
```

Schedules or cancels a wake-up call.

**Parameters:**

| Field | Type | Description |
|-------|------|-------------|
| `room` | string | Room number |
| `time` | string | Wake-up time in HH:MM format |
| `enabled` | boolean | `true` to schedule, `false` to cancel |

**Example:**
```
POST /API/setwakeup?room=2129&time=07:00&enabled=true
```

---

### Call Detail Records (CDR)

```
POST /API/CDR
```

Receives CDR from PBX for posting to the PMS billing system.

> **Note:** This endpoint is for **outbound** data from PBX to TigerTMS, used for call billing integration.

**Parameters (JSON body):**

```json
{
  "src": "2129",
  "dst": "+14165551234",
  "start": "2026-01-05T12:30:00Z",
  "duration": 180,
  "billsec": 165,
  "disposition": "ANSWERED"
}
```

See [Asterisk 12 CDR Specification](https://wiki.asterisk.org/wiki/display/AST/Asterisk+12+CDR+Specification) for full field reference.

---

## Configuration

### Tenant Configuration

Add TigerTMS as the PMS protocol in your tenant configuration:

```yaml
tenants:
  - id: hotel-gamma
    name: "Hotel Gamma"
    pms:
      protocol: tigertms
      # TigerTMS pushes to us, so we configure our listen settings
      api:
        # Path prefix for this tenant's endpoints
        path_prefix: "/tigertms/hotel-gamma"
        # Authentication token from TigerTMS
        auth_token: "${TIGERTMS_AUTH_TOKEN}"
    pbx:
      ari_url: "http://pbx.gamma.local:8088/ari"
      api_url: "https://pbx.gamma.local"
      api_key: "${PBX_GAMMA_API_KEY}"
      tenant_id: "gamma"
```

### TigerTMS iLink Configuration

In the TigerTMS iLink admin console, configure the PBX integration:

1. **URL Base**: `https://your-integration-server.example.com/tigertms/hotel-gamma`
2. **Authentication**: Bearer token or API key header
3. **Timeout**: 30 seconds recommended
4. **Retry**: Enable with exponential backoff

---

## Multi-Tenant Routing

When hosting multiple hotels, route requests by path prefix:

```
/tigertms/hotel-alpha/API/setguest  → Tenant: hotel-alpha
/tigertms/hotel-beta/API/setguest   → Tenant: hotel-beta
```

Alternatively, use an `X-Tenant-ID` header if configured.

---

## Event Mapping

| TigerTMS Endpoint | PMS Event Type | Bicom Action |
|-------------------|----------------|--------------|
| `/API/setguest` (checkin=true) | Check-In | Update extension, set caller ID |
| `/API/setguest` (checkin=false) | Check-Out | Clear caller ID, delete voicemail |
| `/API/setmw` | Message Waiting | Update MWI via ARI/API |
| `/API/setdnd` | Do Not Disturb | Set DND via API |
| `/API/setwakeup` | Wake-Up Call | Schedule via Bicom API |
| `/API/setcos` | Class of Service | Update service plan |
| `/API/setsipdata` | Name Update | Update extension caller ID |

---

## Security Considerations

1. **HTTPS Required**: Always use TLS for production deployments
2. **Authentication**: Validate `Authorization` header on all requests
3. **IP Whitelisting**: Optionally restrict to TigerTMS source IPs
4. **Rate Limiting**: Protect against runaway loops or attacks

```yaml
api:
  tigertms:
    # Validate bearer token
    auth_token: "${TIGERTMS_AUTH_TOKEN}"
    # Allowed source IPs (optional)
    allowed_ips:
      - "203.0.113.0/24"
    # Rate limit per tenant
    rate_limit:
      requests_per_minute: 100
```

---

## Error Handling

### Response Codes

| Code | Meaning | Action |
|------|---------|--------|
| `200` | Success | Event processed |
| `400` | Bad Request | Missing/invalid parameters |
| `401` | Unauthorized | Invalid auth token |
| `404` | Not Found | Unknown room/tenant |
| `500` | Server Error | Internal failure (retry later) |

### Error Response Format

```json
{
  "success": false,
  "error": "Room not found",
  "code": "ROOM_NOT_FOUND"
}
```

---

## Monitoring

### Prometheus Metrics

```
# HTTP requests from TigerTMS
http_requests_total{tenant="hotel-gamma", endpoint="/API/setguest"} 1523

# Request latency
http_request_duration_seconds{tenant="hotel-gamma", quantile="0.99"} 0.045

# Error rate
http_errors_total{tenant="hotel-gamma", code="400"} 12
```

### Logging

```json
{
  "level": "info",
  "ts": "2026-01-05T12:30:00Z",
  "msg": "TigerTMS guest check-in processed",
  "tenant_id": "hotel-gamma",
  "room": "2129",
  "guest_name": "Smith, John",
  "source_ip": "203.0.113.50",
  "latency_ms": 23
}
```

---

## Troubleshooting

### Common Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| 401 Unauthorized | Token mismatch | Verify auth token in both systems |
| Room not found | Missing mapping | Create room-to-extension mapping |
| Timeout | Network issue | Check connectivity, increase timeout |
| Events not arriving | Wrong URL | Verify TigerTMS endpoint configuration |

### Testing Endpoints

```bash
# Test guest check-in
curl -X POST "http://localhost:8080/tigertms/hotel-gamma/API/setguest" \
  -H "Authorization: Bearer ${TOKEN}" \
  -d "room=2129&checkin=true&guest=Test+Guest"

# Test message waiting
curl -X POST "http://localhost:8080/tigertms/hotel-gamma/API/setmw" \
  -H "Authorization: Bearer ${TOKEN}" \
  -d "room=2129&mw=true"
```

---

## See Also

- [Architecture Guide](architecture.md)
- [Protocol Reference](protocols.md)
- [Bicom API Reference](bicom-api.md)
- [TigerTMS iLink Documentation](https://www.tigertms.com/ilink)
