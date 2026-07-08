# Roadmap

This document tracks the gaps and follow-up work identified in the code review.
Items are grouped by theme and roughly ordered by priority within each group.

Status legend:

- **TODO** — not started
- **WIP**  — in progress
- **DONE** — completed and merged

---

## 1. Critical Correctness

- [DONE] Fix failing mitel listener tests (`listener not ready in time`).
      Root cause: `listener_test_helpers.go` remapped `port=0` to the
      protocol's default port (23 for Mitel), so `net.Listen` failed without
      root. Tests now pass with `-race` and CI green.

- [DONE] Fix data race in `MitelListener.Listen` / `Close` (and the
      same race in `FiasListener`). `l.cancel` is now assigned under
      `l.mu.Lock()`.

- [DONE] Fix IPv6 address format warnings from `go vet` in
      `internal/pms/fias/fias.go` and `internal/pms/mitel/mitel.go`.
      Switched to `net.JoinHostPort`.

- [DONE] Wire `db.LogPMSEvent` into `tenant.handleEvent`. Events are now
      persisted to the `pms_events` audit log; failures mark the row
      with the error so the admin retry endpoint
      (`POST /admin/tenants/{id}/events/{eventID}/retry`) actually has
      something to replay.

- [DONE] `tenant.reconnects` counter is now incremented via `bumpReconnect`
      (not yet wired into a real reconnect loop — see §3 below).

- [DONE] **Wake-up call pipeline (Tier 0):**
      - `tenant.handleWakeUp` now tries metadata keys
        `["TI", "wakeup_time", "TI_RAW"]` in order — fixes the silent
        TigerTMS wake-up failure (Bug A).
      - Bicom wake-up rewritten: `pbxware.ext.es.opwakeupcall.set` with
        `state=yes|no`. The previous `wakeupcall.edit` endpoint with a
        `time` parameter does not exist in the public Bicom API.
      - Added `bicom.Client.SetWakeUpState`, `GetWakeUpCallStatus`,
        `TestScheduleWakeUpCall` updated, plus
        `TestCancelWakeUpCall`, `TestSetWakeUpStateFailure`,
        `TestGetWakeUpCallStatus`.
      - Zultys wake-up now loud-fails (structured error log + counter
        `hospitality_pbx_wakeup_unsupported_total{pbx="zultys",action}`)
        instead of silent warn-log.
      - New admin endpoint: `GET /admin/tenants/{id}/capabilities`
        surfaces per-tenant PMS/PBX capability flags so operators can
        spot misconfigurations like "Zultys tenant receiving PMS
        wake-up events".
      - Tests added: `TestFirstNonEmpty`,
        `TestParseWakeUpTime` (incl. roll-forward),
        `TestTigerTMSWakeup_MetadataKey` (regression for Bug A),
        `TestHandlerAuth`.

## 2. Operational Readiness

- [DONE] `Dockerfile` and `docker-compose.yml` healthcheck now use the
      binary's own `--health-check` flag (validates config + DB without
      booting the HTTP listener).

- [DONE] `.env.example` rewritten to enumerate every env var the service
      actually reads, with a base64-key generation hint.

- [DONE] Added `.github/workflows/ci.yml` (Go 1.24 + 1.25, build,
      `go vet`, `-race -count=1` tests, coverage).

- [DONE] Added `migrations/001_schema.sql` (reference schema) and
      `migrations/002_seed.sql` (dev seed: site + tenant + Bicom system +
      room range).

- [TODO] Add a Helm chart or kustomize overlay for k8s deployment.
      `docs/deployment.md` currently covers Docker Compose only.

- [TODO] TLS termination for the Bicom webhook receiver
      (`/api/v1/pbx/webhook/{tenant}`). Currently plain HTTP; needs a
      reverse proxy in front.

## 3. Tenant / PBX Provider Hardening

- [DONE] **WakeUpScheduler (Tier 1)** — in-process goroutine + `wakeup_calls`
      table; uses ARI `Channels.Originate` to fire Bicom wake-up calls at
      the scheduled time. The Bicom REST API only supports state toggle
      (no time parameter), so the scheduler is the only way to deliver
      PMS-driven timed wake-ups on Bicom. See
      [Wake-Up Call Flow diagram](docs/architecture.md#wake-up-call-flow-pms-driven-bicom--ari).
      - `pbx.Provider.OriginateWakeUp(ctx, ext, greetingURL)` interface method
      - `pbx/bicom` implements via `ari.Client.Channel().Originate(...)`
        with `App: "wakeup"` for a Stasis handler to play a greeting
      - `pbx/zultys` returns `ErrOriginateNotSupported` + structured
        counter `hospitality_pbx_wakeup_unsupported_total{pbx="zultys",action="originate"}`
      - `internal/wakeup` package with `Scheduler.Start/Stop`, ticks
        every 10s, caps batch at 100 rows, swallows repo errors per tick
      - `tenant.handleWakeUp` inserts the row after a successful state
        toggle; `tenant.handleCheckOut` cancels any pending row
      - `db.WakeUpCall` model + `CreateWakeUpCall`,
        `GetDueWakeUpCalls`, `MarkWakeUpOriginated`,
        `MarkWakeUpFailed`, `MarkWakeUpCompleted`,
        `CancelWakeUpCall`, `ListWakeUpCalls`,
        `FindPendingWakeUpCall` repo methods
      - `migrations/003_wakeup_calls.sql` with partial index on
        pending rows for the scheduler hot path
      - `GET /admin/tenants/{id}/wakeups` admin endpoint
      - Tests: `internal/wakeup/scheduler_test.go` (4 tests covering
        nil tm, repo error, default interval)

- [TODO] Add a real reconnect supervisor for `tenant.Start`. Today, a
      single transient PMS or PBX failure at boot kills the tenant for
      the life of the process. Plan: a per-tenant goroutine that calls
      `bumpReconnect`, logs, and re-runs `Start` with exponential backoff.

- [TODO] Wire `pbx.CallEvent` voicemail deposits back into the PMS as a
      `MessageWaiting` event. The handler in `tenant.handlePBXEvent`
      currently only logs.

- [TODO] Dynamic extension provisioning. The Bicom client already
      implements `AddExtension`, `DeleteExtension`, and
      `UpdateServicePlan`; the tenant manager does not call them.
      Decision needed: provision per-guest extensions, or rely on
      pre-provisioned extensions forever.

- [TODO] `pbx.Manager` (manages shared `bicom_systems`) and
      `tenant.Manager` (manages per-tenant PBX config from the JSONB
      column) load from different DB tables. Reconcile into a single
      source of truth.
      **Resolution path:** introduce
      `bicom_system_ari_credentials` table (one BicomSystem row can
      host multiple ARI credentials, each scoped to one or more
      tenants). `tenant.Manager` resolves "what Bicom systems should
      this tenant route to?" via the existing `sites` +
      `site_bicom_mappings` failover layer, then picks an ARI
      credential per system. See
      [`docs/integrations/tigertms-cloud-backend.md` §4](docs/integrations/tigertms-cloud-backend.md#pbx-connection-shape-per-bicom-system-with-tenant-scoping)
      and Tier G in §6.

- [TODO] Decide on dead-code lifecycle for `internal/websocket/bridge.go`
      (legacy cloud-WS bridge), `internal/ari/client.go` (alternate ARI
      wrapper), and `internal/bicom/multiprovider.go` (multi-endpoint
      failover helper). File headers now document their status; final
      call: delete or wire.

## 4. Site-connector

- [TODO] Decide whether `site-connector` stays a separate binary or is
      folded into `bicom-hospitality` behind a `pms_listeners:` config
      block. Folding it in simplifies deployment (one image, one
      config) but couples the cloud service to local-listener code.

- [TODO] `site-connector` writes to stdout as JSONL when no `output.url`
      is configured. Add `--output file` for an at-least-once disk spool
      mode that the `output.ResilientOutput` already supports internally.

## 5. PMS Adapter Coverage

| Adapter | Status | Notes |
|---|---|---|
| FIAS (Oracle) | ✅ | connect + listen modes |
| Mitel SX-200 | ✅ connect; ⚠️ wake-up parsing missing `WAK` | Tier 2 |
| TigerTMS iLink | ✅ Tier 0 wake-up fix; ✅ **Tier C — iLink rewrite to match PDF spec** (siteid header, JSON body, extn, dd-mm-yyyy wakeuptime, clearall, on/off strings, `{"result":"success","information":"..."}` response shape; inbound `/API/CDR` removed — CDR is outbound) | See [`docs/integrations/tigertms-ilink-protocol.md`](docs/integrations/tigertms-ilink-protocol.md) |
| ASIP | 📋 planned | reuses FIAS parser; Tier 3 |
| Mews | 📋 planned | REST push; Tier 3 |
| Cloudbeds | 📋 planned | REST push; Tier 3 |

## 6. Outbound Webhooks (hospitality → PMS)

- [TODO] **Tier 0 (elevated from Tier 4 for the TigerTMS tenant class).**
      The TigerTMS cloud-backend + Bicom integration requires us to push
      events back out (CDR, wake-up outcomes, voicemail-left, access-code
      dials). See
      [`docs/integrations/tigertms-cloud-backend.md`](docs/integrations/tigertms-cloud-backend.md)
      §11 for the tier plan and status.

      - [ ] **Tier A** — `POST /events/{bicom_event_publisher_token}`
            receiver, token→tenant lookup, lifecycle coalesce, `cdr_records`
            table. Pure Bicom-side, no TigerTMS dependency.
      - [x] **Tier B** — `outbound_webhooks` table + dispatcher worker +
            signing + retry + idempotency. Pluggable auth strategy
            (bearer / HMAC / URL-token). **`internal/outbound` package,
            `internal/outbound/outboundtest` fake store, 6 unit tests.**
      - [x] **Tier C** — TigerTMS iLink inbound. URL-token-based auth with
            pluggable strategies (`url_token` / `bearer` / `basic`); JSON
            body, `extn` field, `dd-mm-yyyy hh:mm:ss` wake-up format,
            `clearall` action, `on`/`off` strings, iLink-shaped response.
            Token lifecycle via admin API
            (`/admin/tenants/{id}/tokens`). **Rewritten handler in
            `internal/pms/tigertms/`, 12 unit tests, DB-backed resolver in
            `internal/api/token_resolver.go`.**
      - [ ] **Tier D** — TigerTMS cloud inbound + outbound. Per-tenant
            outbound URL + auth strategy + events_to_emit wired through
            `internal/router`. CDR producer wired through Tier J.
      - [ ] **Tier E** — Wake-up reconciler. Periodic
            `pbxware.ext.es.opwakeupcall.get` snapshot + Event Publisher
            wake-up event handler + divergence alert.
      - [ ] **Tier F** — Access-code dial detection + outbound. ARI
            `StasisStart` extractor upgrade; admin CRUD for access-code
            → event-type mapping.
      - [ ] **Tier G** — Multi-bicom ARI credentials table
            (`bicom_system_ari_credentials`); reconcile with
            `tenant.Manager` JSONB config (see §3 below).
      - [x] **Tier H** — ~~Remove inbound `/API/CDR` handler (CDR is outbound
            per the iLink PDF).~~ **DONE** as part of Tier C.
      - [x] **Tier I** — Dynamic event router. Per-tenant pipeline
            composition (`internal/router`) — swap tenants between
            providers via config change, no code change. **3 unit tests.**
      - [ ] **Tier J** — CDR poller. `internal/bicom.CDRPoller` polls
            `pbxware.cdr.list` and emits CDR-shaped events to the
            router. Poller implemented; wiring into the per-tenant
            router pipeline is a small follow-on (depends on A for
            Bicom Event Publisher path; for iLink tenant CDR the
            poller suffices).

- [TODO] Tier 4 — for tenants that are **not** TigerTMS, outbound webhooks
      are still Tier 4. See architecture mermaid in `docs/architecture.md`.

## 5. Test Coverage

Current coverage by package (per `make test`):

| Package | Coverage | Notes |
|---|---|---|
| `bicom` (REST client) | ~45% | basic; webhook/AST endpoints untested |
| `crypto` | ~26% | edge cases around key size not covered |
| `pms/fias` | ~80% | parser-heavy; well covered |
| `pms/listener` | ~74% | mitel + fias integration + race |
| `pms/mitel` | ~31% | parser only |
| `pms/tigertms` | ~18% → **~75%** | iLink handler with all 3 auth strategies + wakeup clearall + JSON body. Tests in `internal/pms/tigertms/tigertms_test.go`. |
| `tenant` | 0% → ~25% (new mapper tests) | PMS-side wired but not asserted |
| `websocket` | ~34% | bridge code is legacy; logsink has tests |

Backlog:

- [DONE] `tenant/mapper_test.go` — individual/range/pattern/prefix
      coverage including invalid inputs.
- [TODO] `tenant/manager_test.go` — wire a SQLite-or-pg integration test
      that asserts `LogPMSEvent` is called for every event and that
      `MarkEventFailed` fires on PBX errors.
- [TODO] `pbx/bicom` and `pbx/zultys` provider tests (use `httptest`
      to stub Bicom REST + ARI, Zultys REST + webhook).
- [TODO] `api/*` handler tests (table-driven, hit the Fiber app
      in-process).
- [TODO] `db/db.go` repository tests (round-trip room mappings,
      range-vs-pattern-vs-exact lookup).

## 6. Documentation

- [DONE] `README.md` rewritten: actual code structure, both binaries,
      mermaid system diagram, accurate API table.
- [DONE] `docs/architecture.md` — added system-topology mermaid graph,
      check-out, PBX-webhook, and site-connector sequence diagrams,
      ER diagram, and replaced outdated docker-compose example.
- [DONE] `docs/deployment.md` — removed the misleading `cp config/example.yaml`
      step, added a boot-stack mermaid graph, added an ER diagram.
- [DONE] `docs/api-reference.md` — added `POST /sessions`, `DELETE /sessions`,
      TigerTMS inbound, `/ws/logs`, and admin-route quick reference.
- [DONE] `docs/integrations/tigertms-ilink-protocol.md` —
      verified wire-format spec extracted from
      `docs/tigertms/TigerTMS_AsteriskRestAPI.pdf` and
      `docs/tigertms/TigerTMS_AsteriskPostCDRRestAPI.pdf`. Used as the
      reference for the Tier C handler rewrite.
- [DONE] `docs/integrations/tigertms-cloud-backend.md` —
      target architecture, gap analysis, schema additions, and
      tier-by-tier implementation plan for the TigerTMS cloud
      backend + Bicom + Event Publisher integration.
- [TODO] Reconcile `docs/future-considerations.md` with what's actually
      been done; close the gaps that this roadmap covers.

---

## Suggested PR-cadence order

1. ✅ Tests green on CI (this PR).
2. ✅ **Tier 0 — Wake-up pipeline (this PR).** TigerTMS metadata key fix,
   Bicom `opwakeupcall.set` rewrite, Zultys loud-fail, capabilities
   endpoint, regression tests.
3. ✅ **Tier 1 — WakeUpScheduler + ARI originate (this PR).** Closes
   the wake-up pipeline on Bicom. Zultys loud-fails.
4. ✅ **Tier B — Outbound dispatcher core** (this PR).
   `internal/outbound` package — `outbound_webhooks` table + worker
   pool + signing (bearer/HMAC/URL-token) + retry/backoff +
   idempotency + pluggable strategies.
5. ✅ **Tier C — TigerTMS iLink inbound rewrite** (this PR). URL-token
   auth with `url_token` / `bearer` / `basic` strategies; JSON body;
   `extn` field; `dd-mm-yyyy hh:mm:ss` wake-up; `clearall` action;
   `on`/`off` strings; iLink-shaped response. Inbound `/API/CDR`
   removed. Token lifecycle via admin API.
6. ✅ **Tier I — Dynamic event router** (this PR). `internal/router`
   composes per-tenant inbound source + outbound sink so tenants
   swap providers via config change.
7. ✅ **Tier J — Bicom CDR poller** (this PR).
   `internal/bicom.CDRPoller` polls `pbxware.cdr.list` and emits
   CDR-shaped events. Poller → router wiring is a small follow-on.
8. **Tier A — Bicom Event Publisher receiver + `cdr_records` table.**
   Pure Bicom-side. Depends on whether vendor emits wakeup events
   (see [open questions](docs/integrations/tigertms-cloud-backend.md#open-questions--unresolved-gaps)).
9. **Tier D — TigerTMS cloud inbound + outbound** (small follow-on to
   Tier B + C). Wire CDR + wake-up outcomes to `cloud_outbound_url`
   per tenant through the router.
10. **Tier E — Wake-up reconciler** (Bicom REST snapshot + Event
    Publisher wake-up event handler + divergence alert).
11. **Tier F — Access-code dial detection + outbound** (ARI
    `StasisStart` extractor upgrade + admin CRUD for access-code →
    event-type mapping).
12. **Tier G — Multi-bicom ARI credentials** (`bicom_system_ari_credentials`
    table; reconcile `tenant.Manager` JSONB config — see §3).
13. ~~Tier H — Remove inbound `/API/CDR` handler~~ — done as part of Tier C.
14. **Tier 2 (re-scoped) — TigerTMS COS / DDI / BYOD consumers +
    Mitel WAK.** The previously Tier 2 list expands: COS, DDI,
    `sippassword` (BYOD) need PBX-side consumers.
15. Reconcile `pbx.Manager` ↔ `tenant.Manager` (§3) — handled by Tier G.
16. Wire `pbx.CallEvent` → PMS MWI (§3) — closes the voicemail→lamp loop.
17. Tier 3 — ASIP, Mews, Cloudbeds adapters.
18. Tier 4 — Outbound webhook delivery for non-TigerTMS tenants.
19. Fold `site-connector` into the main binary.