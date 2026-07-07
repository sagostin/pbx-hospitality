# Data Flow & Architecture Index

Single-page index of every data flow in the service, with the
mermaid diagrams inline. Read this first if you're onboarding; each
section points at the canonical doc for the long-form description.

---

## 1. System Topology

Two binaries (`bicom-hospitality`, `site-connector`) plus the
supported PMS / PBX shapes.

```mermaid
flowchart TB
    subgraph Hotel["Hotel site"]
        PMS[PMS<br/>Mitel / FIAS / TigerTMS / Mews / Cloudbeds]
        SC[site-connector<br/>PMS listener agent]
    end

    subgraph Cloud["Hospitality service"]
        API[/Fiber HTTP<br/>REST + Admin API/]
        TM[Tenant Manager<br/>per-tenant pipeline]
        Sched[WakeUpScheduler<br/>in-process, 10s tick]
        DB[(PostgreSQL<br/>sites · tenants<br/>rooms · sessions<br/>pms_events · wakeup_calls)]
    end

    subgraph PBX["PBX"]
        Bicom[Bicom PBXware<br/>ARI + REST API]
        Zultys[Zultys MX<br/>REST + Webhook]
        FS[FreeSWITCH<br/>sidecar<br/>(planned for Zultys wake-up)]
    end

    PMS -. "TCP (FIAS, Mitel)" .-> SC
    PMS -. "HTTP push (TigerTMS)" .-> API
    SC -. "HTTP/WS to upstream" .-> API

    API --> TM
    TM <--> DB
    TM --> Sched
    Sched --> DB
    TM --> Bicom
    TM --> Zultys
    TM -. "Tier 5" .-> FS
    FS -. "SIP" .-> Zultys

    Bicom -- "ARI events" --> TM
    Zultys -- "webhook" --> API
```

Canonical docs: [architecture.md](architecture.md), [pbx-providers.md](pbx-providers.md).

---

## 2. Per-Tenant PMS Event Pipeline

How a single PMS event flows from the wire into a PBX action.

```mermaid
flowchart LR
    P[PMS wire<br/>TCP socket or HTTP push]
    A[pms.Adapter<br/>parse + ACK]
    TM[tenant.handleEvent]
    DB[(pms_events<br/>audit log)]
    H{{Event type}}
    PBX[pbx.Provider]
    GS[(guest_sessions)]

    P --> A
    A --> TM
    TM --> DB
    TM --> H
    H -- "CheckIn" --> PBX
    H -- "CheckIn" --> GS
    H -- "CheckOut" --> PBX
    H -- "CheckOut" --> GS
    H -- "MWI / DND / WakeUp / NameUpdate" --> PBX
```

Canonical docs: [architecture.md#event-processor](architecture.md).

---

## 3. Wake-Up Call Pipeline (Tier 0 + Tier 1)

```mermaid
sequenceDiagram
    autonumber
    participant PMS as PMS
    participant Adapter as pms.Adapter
    participant Tenant as Tenant.handleWakeUp
    participant REST as Bicom REST API
    participant DB as wakeup_calls
    participant Sched as WakeUpScheduler
    participant ARI as ARI / SIP

    PMS->>Adapter: wake-up event<br/>Metadata: {TI / wakeup_time / TI_RAW}
    Adapter->>Tenant: pms.Event{Type: EventWakeUp}
    Tenant->>Tenant: parseWakeUpTime(timeStr) → time.Time
    Tenant->>REST: POST pbxware.ext.es.opwakeupcall.set state=yes
    REST-->>Tenant: {success: true}
    Tenant->>DB: INSERT wakeup_calls status='pending'
    Note over Sched: tick every 10s
    Sched->>DB: SELECT pending where scheduled_at <= NOW()
    Sched->>ARI: Channels.Originate(<br/>endpoint=PJSIP/{ext},<br/>App=wakeup, Timeout=30s)
    ARI-->>Sched: channel accepted
    Sched->>DB: UPDATE status='originated', originated_at=NOW()
    Note over ARI: rings the room; Stasis app can play greeting
```

Canonical doc: [pbx-providers.md#wake-up-call-pipeline-tier-0--tier-1](pbx-providers.md).

---

## 4. Guest Check-Out Side Effects

```mermaid
sequenceDiagram
    autonumber
    participant PMS as PMS
    participant Tenant as Tenant.handleEvent
    participant PBX as pbx.Provider
    participant DB as PostgreSQL
    participant ARI as ARI / SIP

    PMS->>Tenant: pms.Event{Type: EventCheckOut, Room: 101}
    Tenant->>PBX: UpdateExtensionName(ext, "")
    Tenant->>PBX: ClearVoicemailForGuest(ext)<br/>(delete + reset greeting)
    Tenant->>PBX: CancelWakeUpCall(ext)
    Tenant->>DB: FindPendingWakeUpCall(tenant, room)
    Tenant->>DB: UPDATE wakeup_calls SET status='cancelled'
    Tenant->>PBX: SetMWI(ext, off)
    Tenant->>DB: UPDATE guest_sessions SET check_out=NOW()
```

Canonical doc: [architecture.md#check-out-flow-cleanup--db-end-of-session](architecture.md).

---

## 5. PBX → PMS Reverse Flow (voicemail webhook → MWI)

```mermaid
sequenceDiagram
    autonumber
    participant PBX as PBX (Zultys / Bicom)
    participant API as /api/v1/pbx/webhook/{tenant}
    participant Provider as pbx.Provider<br/>(WebhookProvider)
    participant Tenant as Tenant Event Processor

    PBX->>API: POST {event:"voicemail_left", extension:"1101"}
    API->>Provider: HandleWebhook(req)
    Provider->>Provider: validate HMAC signature
    Provider->>Provider: mapWebhookEventToCallEvent
    Provider-->>Tenant: events <- CallEvent{Type: VoicemailLeft}
    Tenant->>Tenant: (currently logs only — Tier 2 wires MWI back to PMS)
```

Canonical doc: [architecture.md#pbx--pms-reverse-flow-voicemail-webhook--mwi](architecture.md).

---

## 6. site-connector Forwarding Flow

```mermaid
sequenceDiagram
    autonumber
    participant PMS as PMS (FIAS / Mitel)
    participant Listener as site-connector Listener
    participant Buffer as Spool Buffer (disk)
    participant Output as Resilient Output
    participant API as Hospitality /api/v1/pbx/webhook/{site}

    PMS->>Listener: GI|RN1015|GNSmith|
    Listener->>Listener: parse → pms.Event{Type: CheckIn}
    Listener->>Output: EventEnvelope{protocol, event}
    alt URL reachable
        Output->>API: POST (HTTPS or WSS)
        API-->>Output: 200 OK
    else URL unreachable
        Output->>Buffer: append to disk
        Note over Buffer: batch retries with backoff
    end
```

Canonical doc: [architecture.md#site-connector-forwarding-flow](architecture.md).

---

## 7. Protocol Topology (PMS-side)

```mermaid
flowchart LR
    subgraph Connect["Client mode (we connect to PMS)"]
        C_FIAS["fias.Adapter<br/>→ tcp:PMS:5000"]
        C_MITEL["mitel.Adapter<br/>→ tcp:PMS:23"]
    end

    subgraph Server["Server mode (PMS connects to us)"]
        S_FIAS["listener.FiasListener<br/>tcp:0.0.0.0:5000"]
        S_MITEL["listener.MitelListener<br/>tcp:0.0.0.0:23"]
    end

    subgraph Push["HTTP push (middleware POSTs)"]
        T_TIGER["tigertms.Handler<br/>POST /tigertms/{tenant}/API/*"]
    end

    PMS[PMS / Middleware]

    PMS -- "TCP" --> C_FIAS
    PMS -- "TCP" --> C_MITEL
    PMS -- "TCP" --> S_FIAS
    PMS -- "TCP" --> S_MITEL
    PMS -- "HTTP" --> T_TIGER
```

Canonical doc: [protocols.md](protocols.md).

---

## 8. PBX Capability Surface

| Capability | Bicom | Zultys |
|---|---|---|
| `SupportsWakeUpCalls` | ✅ (state toggle) | ❌ |
| `SupportsWakeUpOrigination` | ✅ (ARI Channels.Originate) | ❌ |
| `SupportsVoicemailGreeting` | ✅ | ✅ |
| `SupportsCallForward` | ✅ | ✅ |
| `SupportsMWI` | ✅ | ✅ |
| `SupportsDND` | ✅ | ✅ |
| `SupportsInboundEvents` | ✅ (ARI) | ✅ (webhook) |

```mermaid
flowchart LR
    IFACE[pbx.Provider interface]
    Bicom[bicom.Provider]
    Zultys[zultys.Provider]

    IFACE -- "Connect / Close / Connected" --> Bicom
    IFACE -- "Connect / Close / Connected" --> Zultys
    IFACE -- "UpdateExt / SetMWI / SetDND /<br/>SetCallForward / SetVoicemailGreeting" --> Bicom
    IFACE -- "UpdateExt / SetMWI / SetDND /<br/>SetCallForward / SetVoicemailGreeting" --> Zultys
    IFACE -- "ScheduleWakeUpCall (state toggle)" --> Bicom
    IFACE -- "ScheduleWakeUpCall → ErrWakeUpNotSupported" --> Zultys
    IFACE -- "OriginateWakeUp via ARI" --> Bicom
    IFACE -- "OriginateWakeUp → ErrOriginateNotSupported" --> Zultys
```

Canonical doc: [pbx-providers.md](pbx-providers.md).

---

## 9. Admin API Surface

```mermaid
graph LR
    subgraph Tenants["/admin/tenants"]
        T1[CRUD]
        T2[/:id/rooms/]
        T3[/:id/sessions/]
        T4[/:id/events/]
        T5[/:id/wakeups/]
        T6[/:id/health]
        T7[/:id/capabilities]
        T8[/import]
    end

    subgraph Sites["/admin/sites"]
        S1[CRUD]
        S2[/:id/bicom]
        S3[/:id/health]
    end

    subgraph PBX["/admin/bicom-systems"]
        B1[CRUD]
        B2[/:id/ari-secret]
    end

    subgraph Mgr["/admin/pbx"]
        M1[GET /status]
        M2[POST /reload]
        M3[POST /:id/reload]
    end

    Inbound[Inbound PMS endpoints]
    WS[/ws/logs]

    Tenants --- Sites
    Sites --- PBX
    PBX --- Mgr
    Mgr --- Inbound
    Inbound --- WS
```

Canonical docs: [api-reference.md](api-reference.md), [admin-api.md](admin-api.md).

---

## 10. Database Schema

```mermaid
erDiagram
    sites ||--o{ tenants : hosts
    sites ||--o{ site_bicom_mappings : "uses"
    bicom_systems ||--o{ site_bicom_mappings : "used by"
    tenants ||--o{ room_mappings : "has"
    tenants ||--o{ guest_sessions : "tracks"
    tenants ||--o{ pms_events : "audit log"
    tenants ||--o{ wakeup_calls : "scheduler queue"

    sites {
        string id PK
        string name
        string auth_code
    }
    tenants {
        string id PK
        string site_id FK
        string name
        jsonb pms_config
        jsonb pbx_config
        bool enabled
    }
    bicom_systems {
        string id PK
        string name
        string api_url
        string api_key
        bytes ari_pass_encrypted
        bytes ari_pass_nonce
    }
    room_mappings {
        int id PK
        string tenant_id FK
        string room_number
        string room_end
        string extension
        string extension_end
        string match_pattern
    }
    guest_sessions {
        int id PK
        string tenant_id FK
        string room_number
        string extension
        string guest_name
        timestamp check_in
        timestamp check_out
    }
    pms_events {
        bigint id PK
        string tenant_id FK
        string event_type
        string room_number
        string extension
        bool processed
        text error
    }
    wakeup_calls {
        bigint id PK
        string tenant_id FK
        string extension
        timestamp scheduled_at
        string status
        int attempt_count
        text last_error
        timestamp originated_at
        timestamp completed_at
    }
```

Canonical docs: [migrations/001_schema.sql](../migrations/001_schema.sql), [migrations/003_wakeup_calls.sql](../migrations/003_wakeup_calls.sql), [architecture.md](../docs/architecture.md).

---

## 11. Read-Order Index

If you're onboarding to the codebase, read in this order:

1. [README.md](../README.md) — what the service is, quick start
2. This file (DATA-FLOW.md) — how data flows
3. [architecture.md](architecture.md) — full sequence diagrams + DB schema
4. [pbx-providers.md](pbx-providers.md) — provider capabilities, wake-up pipeline
5. [protocols.md](protocols.md) — PMS wire format reference
6. [api-reference.md](api-reference.md) + [admin-api.md](admin-api.md) — HTTP surface
7. [deployment.md](deployment.md) — go from code to running service
8. [ROADMAP.md](../ROADMAP.md) — what's done, what's next