# Data Flow & Architecture Index

Single-page index of every data flow in the service, with the
mermaid diagrams inline. Read this first if you're onboarding; each
section points at the canonical doc for the long-form description.

Mermaid conventions used in this file:
- Node labels in flowcharts may use `<br/>` for line breaks.
- Edge labels are single-line only.
- Sequence diagram messages may use `<br/>` for line breaks.
- Participant aliases in sequence diagrams are single-line.

---

## 1. System Topology

Two binaries (`bicom-hospitality`, `site-connector`) plus the
supported PMS / PBX shapes.

```mermaid
flowchart TB
    subgraph Hotel["Hotel site"]
        PMS["PMS - Mitel / FIAS / TigerTMS / Mews / Cloudbeds"]
        SC["site-connector - PMS listener agent"]
    end

    subgraph Cloud["Hospitality service"]
        API["Fiber HTTP - REST + Admin API"]
        TM["Tenant Manager - per-tenant pipeline"]
        Sched["WakeUpScheduler - in-process, 10s tick"]
        DB[("PostgreSQL - sites, tenants, rooms, sessions, pms_events, wakeup_calls")]
    end

    subgraph PBX["PBX"]
        Bicom["Bicom PBXware - ARI + REST API"]
        Zultys["Zultys MX - REST + Webhook"]
        FS["FreeSWITCH sidecar - planned for Zultys wake-up"]
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
    P["PMS wire - TCP socket or HTTP push"]
    A["pms.Adapter - parse + ACK"]
    TM["tenant.handleEvent"]
    DB[("pms_events - audit log")]
    H{{Event type}}
    PBX["pbx.Provider"]
    GS[("guest_sessions")]

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
    participant PMS
    participant Adapter as "pms.Adapter"
    participant Tenant as "Tenant.handleWakeUp"
    participant REST as "Bicom REST API"
    participant DB as "wakeup_calls"
    participant Sched as "WakeUpScheduler"
    participant ARI as "ARI / SIP"

    PMS->>Adapter: wake-up event with metadata TI / wakeup_time / TI_RAW
    Adapter->>Tenant: pms.Event type EventWakeUp
    Tenant->>Tenant: parseWakeUpTime to time.Time
    Tenant->>REST: POST pbxware.ext.es.opwakeupcall.set state=yes
    REST-->>Tenant: success true
    Tenant->>DB: INSERT wakeup_calls status=pending
    Note over Sched: tick every 10s
    Sched->>DB: SELECT pending where scheduled_at <= NOW
    Sched->>ARI: Channels.Originate endpoint PJSIP/ext App wakeup Timeout 30s
    ARI-->>Sched: channel accepted
    Sched->>DB: UPDATE status=originated originated_at=NOW
    Note over ARI: rings the room, Stasis app can play greeting
```

Canonical doc: [pbx-providers.md#wake-up-call-pipeline-tier-0--tier-1](pbx-providers.md).

---

## 4. Guest Check-Out Side Effects

```mermaid
sequenceDiagram
    autonumber
    participant PMS
    participant Tenant as "Tenant.handleEvent"
    participant PBX as "pbx.Provider"
    participant DB as "PostgreSQL"
    participant ARI as "ARI / SIP"

    PMS->>Tenant: pms.Event type EventCheckOut Room 101
    Tenant->>PBX: UpdateExtensionName ext empty
    Tenant->>PBX: ClearVoicemailForGuest ext (delete + reset greeting)
    Tenant->>PBX: CancelWakeUpCall ext
    Tenant->>DB: FindPendingWakeUpCall tenant room
    Tenant->>DB: UPDATE wakeup_calls SET status=cancelled
    Tenant->>PBX: SetMWI ext off
    Tenant->>DB: UPDATE guest_sessions SET check_out=NOW
```

Canonical doc: [architecture.md#check-out-flow-cleanup--db-end-of-session](architecture.md).

---

## 5. PBX → PMS Reverse Flow (voicemail webhook → MWI)

```mermaid
sequenceDiagram
    autonumber
    participant PBX as "PBX (Zultys / Bicom)"
    participant API as "HTTP /api/v1/pbx/webhook/{tenant}"
    participant Provider as "pbx.Provider (WebhookProvider)"
    participant Tenant as "Tenant Event Processor"

    PBX->>API: POST voicemail_left extension 1101
    API->>Provider: HandleWebhook
    Provider->>Provider: validate HMAC signature
    Provider->>Provider: mapWebhookEventToCallEvent
    Provider-->>Tenant: events channel CallEvent VoicemailLeft
    Note over Tenant: currently logs only - Tier 2 wires MWI back to PMS
```

Canonical doc: [architecture.md#pbx--pms-reverse-flow-voicemail-webhook--mwi](architecture.md).

---

## 6. site-connector Forwarding Flow

```mermaid
sequenceDiagram
    autonumber
    participant PMS as "PMS (FIAS / Mitel)"
    participant Listener as "site-connector Listener"
    participant Buffer as "Spool Buffer (disk)"
    participant Output as "Resilient Output"
    participant API as "Hospitality webhook endpoint"

    PMS->>Listener: GI record RN1015 GNSmith
    Listener->>Listener: parse to pms.Event type CheckIn
    Listener->>Output: EventEnvelope protocol and event
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
    subgraph Connect["Client mode - we connect to PMS"]
        C_FIAS["fias.Adapter<br/>tcp to PMS port 5000"]
        C_MITEL["mitel.Adapter<br/>tcp to PMS port 23"]
    end

    subgraph Server["Server mode - PMS connects to us"]
        S_FIAS["listener.FiasListener<br/>tcp 0.0.0.0 port 5000"]
        S_MITEL["listener.MitelListener<br/>tcp 0.0.0.0 port 23"]
    end

    subgraph Push["HTTP push - middleware POSTs"]
        T_TIGER["tigertms.Handler<br/>POST /tigertms/{tenant}/API/*"]
    end

    PMS["PMS / Middleware"]

    PMS -- "TCP" --> C_FIAS
    PMS -- "TCP" --> C_MITEL
    PMS -- "TCP" --> S_FIAS
    PMS -- "TCP" --> S_MITEL
    PMS -- "HTTP" --> T_TIGER
```

Canonical doc: [protocols.md](protocols.md).

---

## 8. PBX Capability Surface

```mermaid
flowchart LR
    subgraph Lifecycle["Lifecycle methods"]
        L1["Connect / Close / Connected"]
    end

    subgraph Common["Common operations"]
        C1["UpdateExtensionName"]
        C2["SetMWI"]
        C3["SetDND"]
        C4["SetCallForward"]
        C5["ClearVoicemailForGuest"]
    end

    subgraph Wakeup["Wake-up calls"]
        W1["ScheduleWakeUpCall<br/>state toggle only"]
        W2["OriginateWakeUp<br/>fires the actual call"]
    end

    IFACE["pbx.Provider interface"]

    Bicom["bicom.Provider"]
    Zultys["zultys.Provider"]

    IFACE -- Lifecycle --> Bicom
    IFACE -- Lifecycle --> Zultys

    IFACE -- Common operations --> Bicom
    IFACE -- Common operations --> Zultys

    IFACE -- W1 --> Bicom
    IFACE -- W1 --> Zultys
    IFACE -- W2 --> Bicom
    IFACE -- W2 --> Zultys

    Bicom -- "W1 returns success (state=yes)" --> Note1["Bicom REST accepts<br/>state toggle"]
    Bicom -- "W2 returns ARI originate success" --> Note2["ARI Channels.Originate<br/>to the extension"]
    Zultys -- "W1 returns ErrWakeUpNotSupported" --> Note3["Zultys has no wake-up API"]
    Zultys -- "W2 returns ErrOriginateNotSupported" --> Note4["Zultys has no SIP originate"]
```

See the [Bicom](#bicom-capabilities) and [Zultys](#zultys-capabilities)
sections below for the full capability matrix.

### Bicom capabilities

```mermaid
flowchart LR
    B["bicom.Provider"]
    CapB["SupportsWakeUpCalls - YES if apiClient<br/>SupportsWakeUpOrigination - YES if ariClient<br/>SupportsVoicemailGreeting - YES<br/>SupportsCallForward - YES<br/>SupportsMWI - YES<br/>SupportsDND - YES<br/>SupportsInboundEvents - YES if ARI URL set"]
    B --> CapB
```

### Zultys capabilities

```mermaid
flowchart LR
    Z["zultys.Provider"]
    CapZ["SupportsWakeUpCalls - NO<br/>SupportsWakeUpOrigination - NO<br/>SupportsVoicemailGreeting - YES<br/>SupportsCallForward - YES<br/>SupportsMWI - YES<br/>SupportsDND - YES<br/>SupportsInboundEvents - YES"]
    Z --> CapZ
```

Canonical doc: [pbx-providers.md](pbx-providers.md).

---

## 9. Admin API Surface

```mermaid
flowchart TB
    Tenants["/admin/tenants - CRUD + per-tenant sub-resources"]
    Sites["/admin/sites - CRUD + per-site Bicom mappings"]
    BicomSys["/admin/bicom-systems - CRUD + ARI secret rotation"]
    PBXMgr["/admin/pbx - status + reload"]
    Inbound["Inbound PMS endpoints - TigerTMS routes + admin API key"]
    WS["/ws/logs - WebSocket log tail"]

    Tenants --> Sites
    Sites --> BicomSys
    BicomSys --> PBXMgr
    PBXMgr --> Inbound
    Inbound --> WS
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

Canonical docs: [migrations/001_schema.sql](../migrations/001_schema.sql),
[migrations/003_wakeup_calls.sql](../migrations/003_wakeup_calls.sql),
[architecture.md](../docs/architecture.md).

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