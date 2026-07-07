# PBX Providers

This document describes the PBX provider abstraction layer and the supported PBX backends.

## Overview

The hospitality integration supports multiple PBX systems through a provider abstraction layer. Each provider implements a common interface for hospitality operations while handling the specifics of its PBX platform.

```mermaid
graph TB
    subgraph "PBX Providers"
        BICOM[Bicom Provider<br/>ARI + REST API]
        ZULTYS[Zultys Provider<br/>Session Auth + Webhooks]
        FUTURE[Future Providers<br/>FreeSWITCH, 3CX, etc.]
    end
    
    subgraph "Provider Interface"
        IFACE[pbx.Provider<br/>- UpdateExtensionName<br/>- SetMWI / SetDND<br/>- ScheduleWakeUpCall<br/>- ClearVoicemailForGuest]
    end
    
    subgraph "Inbound Events"
        ARI[ARI WebSocket]
        WEBHOOK[POST /api/v1/pbx/webhook]
    end
    
    BICOM --> IFACE
    ZULTYS --> IFACE
    FUTURE --> IFACE
    ARI --> BICOM
    WEBHOOK --> ZULTYS
```

---

## Supported Providers

| Provider | Type | Inbound Events | Outbound Commands |
|----------|------|----------------|-------------------|
| **Bicom** | Asterisk-based | ARI WebSocket | REST API + ARI |
| **Zultys** | Webhook-based | HTTP POST | Session-authenticated REST |

---

## Bicom PBXware

Bicom PBXware is the primary PBX backend, using Asterisk's ARI for real-time events and the Bicom REST API for configuration.

### Configuration

```yaml
tenants:
  - id: hotel-alpha
    pbx:
      type: bicom              # Default, can be omitted
      
      # Bicom REST API (for extension/voicemail management)
      api_url: "https://pbx.example.com"
      api_key: "${BICOM_API_KEY}"
      tenant_id: "alpha"
      
      # ARI (for real-time call events and MWI)
      ari_url: "http://pbx:8088/ari"
      ari_ws_url: "ws://pbx:8088/ari/events"
      ari_user: "hospitality"
      ari_pass: "${ARI_SECRET}"
      app_name: "bicom-hospitality"
```

### Features

| Feature | API Used | Notes |
|---------|----------|-------|
| Extension Name | REST API | `pbxware.ext.edit` |
| Voicemail Delete | REST API | `pbxware.vm.delete_all` |
| Voicemail Greeting | REST API | `pbxware.ext.es.vm.edit` |
| MWI | ARI | Mailbox state update |
| DND | REST API | `pbxware.ext.es.dnd.edit` |
| Wake-Up State | REST API | `pbxware.ext.es.opwakeupcall.set` (state-only) |
| Wake-Up Ring | ARI | `Channels.Originate` via WakeUpScheduler (Tier 1) |
| Call Forward | REST API | `pbxware.ext.es.callfwd.set` |

### Capabilities

`pbx.ProviderWithCapabilities.Capabilities()` returns:

| Capability | Bicom | Notes |
|---|---|---|
| `SupportsWakeUpCalls` | ✅ (if apiClient) | toggles the PBX wake-up state |
| `SupportsWakeUpOrigination` | ✅ (if ariClient) | ARI `Channels.Originate` |
| `SupportsVoicemailGreeting` | ✅ (if apiClient) | |
| `SupportsCallForward` | ✅ (if apiClient) | |
| `SupportsMWI` | ✅ | ARI mailbox update |
| `SupportsDND` | ✅ | REST API |
| `SupportsInboundEvents` | ✅ (if ARI URL set) | ARI WebSocket + HTTP webhook |

Inspect at runtime:

```bash
curl -H "X-Admin-Key: $KEY" http://localhost:8080/admin/tenants/hotel-alpha/capabilities
```

```json
{
  "pbx": {
    "supports_wake_up_calls": true,
    "supports_wake_up_origination": true,
    "supports_voicemail_greeting": true,
    "supports_call_forward": true,
    "supports_mwi": true,
    "supports_dnd": true,
    "supports_inbound_events": true
  },
  "pms": { "protocol": "fias", "connected": true },
  "notes": [
    "Wake-up state is toggled on the PBX via REST. The actual ring-at-HH:MM is performed by the WakeUpScheduler via ARI Originate."
  ]
}
```

### Wake-Up Call Pipeline (Tier 0 + Tier 1)

```mermaid
sequenceDiagram
    autonumber
    participant PMS as PMS<br/>(FIAS / TigerTMS / Mitel / Mews / Cloudbeds)
    participant Adapter as PMS Adapter
    participant Tenant as Tenant.handleWakeUp
    participant REST as Bicom REST API
    participant DB as wakeup_calls table
    participant Sched as WakeUpScheduler<br/>(in-process)
    participant ARI as ARI / SIP

    PMS->>Adapter: wake-up event with time
    Adapter->>Tenant: pms.Event{Type: EventWakeUp,<br/>Metadata: {TI / wakeup_time / TI_RAW}}
    Tenant->>REST: POST pbxware.ext.es.opwakeupcall.set<br/>id={ext}&state=yes
    REST-->>Tenant: success
    Tenant->>DB: INSERT wakeup_calls status='pending'
    Note over Sched: tick every 10s
    Sched->>DB: SELECT pending where scheduled_at <= NOW()
    Sched->>ARI: Channels.Originate(<br/>endpoint=PJSIP/{ext},<br/>App=wakeup, AppArgs={ext},<br/>Timeout=30s)
    ARI-->>Sched: channel accepted
    Sched->>DB: UPDATE status='originated', originated_at=NOW()
    Note over ARI: rings the room; Stasis app can play greeting
```

**Operator setup required:** register a Stasis app named `wakeup` in
PBXware so `App=wakeup` lands somewhere that answers + plays a greeting
+ hangs up. A minimal `[wakeup]` context with
`exten => s,1,Answer() → Wait(2) → Hangup()` is enough; a real
implementation plays a per-tenant greeting.

> **Wake-up calls on Bicom are a two-part operation.** The REST API
> (`opwakeupcall.set state=yes`) toggles whether the extension has a wake-up
> scheduled; the service's **WakeUpScheduler** then uses ARI to originate
> the actual call at the scheduled time. See `docs/architecture.md` and
> `ROADMAP.md` Tier 1 for details.

See [Bicom API Reference](bicom-api.md) for detailed endpoint documentation.

---

## Zultys

Zultys uses session-based authentication and HTTP webhooks for bidirectional communication.

### Configuration

```yaml
tenants:
  - id: hotel-zultys
    pbx:
      type: zultys
      
      # Outbound API (session-authenticated)
      api_url: "https://zultys.hotel.com/api"
      auth_url: "/auth/login"
      username: "${ZULTYS_USERNAME}"
      password: "${ZULTYS_PASSWORD}"
      
      # Inbound webhook validation
      webhook_secret: "${ZULTYS_WEBHOOK_SECRET}"
```

### Session Authentication

The Zultys provider automatically manages authentication:

1. On first API call, authenticates using `username`/`password`
2. Caches the session token
3. Auto-refreshes on expiry or 401 response
4. Never stores raw admin credentials in API calls

### Inbound Webhooks

Zultys sends call events to: `POST /api/v1/pbx/webhook/{tenant-id}`

**Webhook Payload Format:**
```json
{
  "event": "access_code",
  "extension": "1015",
  "caller_id": "5551234567",
  "caller_name": "Guest Room 1015",
  "access_code": "*411",
  "timestamp": "2026-01-05T14:00:00Z"
}
```

**Supported Events:**
| Event | Description |
|-------|-------------|
| `access_code` | Guest dialed a feature code (*411, etc.) |
| `incoming` | Incoming call to room extension |
| `voicemail_left` | Voicemail message left |
| `call_end` | Call ended |

**Signature Validation:**

If `webhook_secret` is configured, requests must include:
```
X-Webhook-Signature: <HMAC-SHA256 hex digest of body>
```

### Features

| Feature | Endpoint | Status |
|---------|----------|--------|
| Extension Name | POST `/extensions/{ext}/name` | ✅ Supported |
| Voicemail Delete | DELETE `/voicemail/{ext}/messages` | ✅ Supported |
| Voicemail Greeting | POST `/voicemail/{ext}/greeting/reset` | ✅ Supported |
| MWI | POST `/extensions/{ext}/mwi` | ✅ Supported |
| DND | POST `/extensions/{ext}/dnd` | ✅ Supported |
| Call Forward | POST `/extensions/{ext}/forward` | ✅ Supported |
| Wake-Up Calls | N/A | ❌ Not supported (state toggle + ring) |
| Wake-Up Origination | N/A | ❌ Not supported |

> **Note:** These are placeholder endpoints. Update with actual Zultys API paths when available.

### Capabilities

| Capability | Zultys | Notes |
|---|---|---|
| `SupportsWakeUpCalls` | ❌ | no schedule API |
| `SupportsWakeUpOrigination` | ❌ | no SIP-originate API |
| `SupportsVoicemailGreeting` | ✅ | |
| `SupportsCallForward` | ✅ | |
| `SupportsMWI` | ✅ | |
| `SupportsDND` | ✅ | |
| `SupportsInboundEvents` | ✅ | HTTP webhook |

### Wake-Up Call Limitation

**Zultys does not provide a native API for scheduled wake-up calls.**
ScheduleWakeUpCall / CancelWakeUpCall / OriginateWakeUp on the Zultys
provider returns `ErrWakeUpNotSupported` / `ErrOriginateNotSupported`
and emits a structured error log + counter
(`hospitality_pbx_wakeup_unsupported_total{pbx="zultys",action="schedule|cancel|originate"}`).

Operators relying on Zultys + PMS-driven wake-ups have three options:

1. **Migrate the tenant to Bicom** (which supports wake-up via the
   Bicom REST API + WakeUpScheduler + ARI).
2. **Run a FreeSWITCH / Asterisk sidecar** that originates wake-up calls
   into the Zultys via SIP and plays a greeting.

```mermaid
flowchart LR
    PMS[PMS<br/>wake-up event]
    H[Hospitality service]
    DB[(wakeup_calls)]
    FS[FreeSWITCH sidecar<br/>sip originate]
    Z[Zultys<br/>SIP leg]

    PMS --> H
    H --> DB
    H -->|"(Tier 4)"| FS
    FS --> Z
```

3. **Let the guest set their own wake-up** via the room phone's feature
   code — the PMS wake-up event will be rejected and the row marked
   `failed`.

You can verify a tenant's capabilities at runtime:

```bash
curl -H "X-Admin-Key: $KEY" http://localhost:8080/admin/tenants/{id}/capabilities
```

The `pbx.supports_wake_up_calls` boolean is `false` for Zultys tenants.
The endpoint returns 200 even when the tenant is disconnected.

---

## Adding a New Provider

To add support for a new PBX (e.g., FreeSWITCH):

### 1. Create the Provider Package

```
internal/pbx/freeswitch/
├── provider.go    # Main provider implementation
└── api.go         # Optional: API client
```

### 2. Implement the Provider Interface

```go
package freeswitch

import "github.com/sagostin/pbx-hospitality/internal/pbx"

type Provider struct {
    // provider fields
}

// NewProvider creates a new provider
func NewProvider(cfg Config) (*Provider, error) {
    // ...
}

// Implement all pbx.Provider methods
func (p *Provider) Connect(ctx context.Context) error { ... }
func (p *Provider) Close() error { ... }
func (p *Provider) Connected() bool { ... }
func (p *Provider) UpdateExtensionName(ctx context.Context, ext, name string) error { ... }
// ... remaining methods
```

### 3. Register the Provider

```go
func init() {
    pbx.Register("freeswitch", func(cfg pbx.ProviderConfig) (pbx.Provider, error) {
        return NewProvider(Config{
            // map config fields
        })
    })
}
```

### 4. Import in Tenant Manager

```go
// internal/tenant/manager.go
import (
    _ "github.com/sagostin/pbx-hospitality/internal/pbx/freeswitch"
)
```

### 5. Add Config Fields

```go
// internal/config/config.go
type PBXConfig struct {
    // ... existing fields
    
    // FreeSWITCH-specific
    FSHost     string `yaml:"fs_host"`
    FSPassword string `yaml:"fs_password"`
}
```

---

## Provider Interface Reference

```go
type Provider interface {
    // Connection lifecycle
    Connect(ctx context.Context) error
    Close() error
    Connected() bool

    // Extension management
    UpdateExtensionName(ctx context.Context, ext, name string) error

    // Voicemail management
    DeleteAllVoicemails(ctx context.Context, ext string) error
    ResetVoicemailGreeting(ctx context.Context, ext string) error
    ClearVoicemailForGuest(ctx context.Context, ext string) error

    // Message Waiting Indicator
    SetMWI(ctx context.Context, ext string, on bool) error

    // Do Not Disturb
    SetDND(ctx context.Context, ext string, on bool) error

    // Wake-up calls
    ScheduleWakeUpCall(ctx context.Context, ext string, wakeTime time.Time) error
    CancelWakeUpCall(ctx context.Context, ext string) error

    // Call forwarding
    SetCallForward(ctx context.Context, ext, destination string, enabled bool) error
}
```

For providers that receive inbound events:

```go
type EventProvider interface {
    Provider
    Events() <-chan CallEvent
}

type WebhookProvider interface {
    EventProvider
    HandleWebhook(r *http.Request) error
    WebhookSecret() string
}
```
