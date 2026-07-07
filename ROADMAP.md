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
| TigerTMS iLink | ✅ Tier 0 wake-up fix; ⚠️ COS / DDI events lost | Tier 2 |
| ASIP | 📋 planned | reuses FIAS parser; Tier 3 |
| Mews | 📋 planned | REST push; Tier 3 |
| Cloudbeds | 📋 planned | REST push; Tier 3 |

## 6. Outbound Webhooks (hospitality → PMS)

- [TODO] Tier 4 — `outbound_webhooks` table + dispatcher + admin CRUD +
      signed delivery + exp-backoff retry. See architecture mermaid in
      `docs/architecture.md`.

## 5. Test Coverage

Current coverage by package (per `make test`):

| Package | Coverage | Notes |
|---|---|---|
| `bicom` (REST client) | ~45% | basic; webhook/AST endpoints untested |
| `crypto` | ~26% | edge cases around key size not covered |
| `pms/fias` | ~80% | parser-heavy; well covered |
| `pms/listener` | ~74% | mitel + fias integration + race |
| `pms/mitel` | ~31% | parser only |
| `pms/tigertms` | ~18% | adapter thin; HTTP handler untested |
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
4. **Tier 2 — TigerTMS round-out + Mitel WAK.** COS / DDI / reservation_id
   handling; add WAK to Mitel parser.
5. Reconcile `pbx.Manager` ↔ `tenant.Manager` (§3) — pick one model.
6. Wire `pbx.CallEvent` → PMS MWI (§3) — closes the voicemail→lamp loop.
7. Tier 3 — ASIP, Mews, Cloudbeds adapters.
8. Tier 4 — Outbound webhook delivery.
9. Fold `site-connector` into the main binary.