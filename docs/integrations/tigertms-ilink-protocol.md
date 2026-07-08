# TigerTMS iLink — Actual Wire Protocol + Inbound Routing

> Source: `docs/tigertms/TigerTMS_AsteriskRestAPI.pdf` (9 pages) and
> `docs/tigertms/TigerTMS_AsteriskPostCDRRestAPI.pdf` (4 pages). Both
> ship with this repo. Transcribed verbatim; field names, examples, and
> response shapes are taken from those documents.

> **This supersedes the "TigerTMS" sections of `docs/tigertms.md` and
> the field-level documentation in `docs/protocols.md`.** The existing
> `internal/pms/tigertms/tigertms.go` handler is **incorrect** for the
> real iLink protocol — see the [gaps doc](./tigertms-cloud-backend.md#critical-gap-existing-tigertms-handler-does-not-match-ilink-protocol).

---

## 1. Role & topology

TigerTMS iLink is **middleware on the hotel LAN**. It speaks the PMS's
native protocol (Mitel, FIAS, etc.) and forwards normalized events to
the PBX (Asterisk / Bicom / us) over HTTP. In the protocol, **we play
the role of the Asterisk IP Switch.**

```
PMS ─(PMS-native)─► TigerTMS iLink (on-prem) ─(HTTPS, this protocol)─► us (Asterisk role)
                                                              ▲
                                                              │ /API/CDR (CDR push back)
us ────────────────────────────────────────────────────────────┘
```

For multi-tenanted hosted solutions, TigerTMS discriminates tenants
via an HTTP header (`siteid`), **not** a path prefix or URL token.

---

## 2. Common transport rules

| Concern | Value |
|---|---|
| Method | `POST` |
| Auth (multi-tenanted) | Long random secret embedded in the URL path: `POST /api/v1/pms/inbound/<token>/API/setguest`. The token identifies AND authenticates the tenant. Optional layered auth: `Authorization: Bearer <secret>` or `Authorization: Basic <user:pass>` — see §2.1. |
| Body | JSON, `Content-Type: text/json` |
| Response (most endpoints) | `{"result":"success","information":"..."}` or `{"result":"failed","information":"..."}` |
| Response (CDR) | `{"response":"RECEIVEDOK"}` or `{"response":"ERROR"}` — different shape, **outbound from us** |
| TLS | Expected in production. iLink operator configures the URL. |

> **Wire-level note from the CDR PDF**: on CDR failure, the Asterisk
> side should buffer and retry, "try 3 times then dump and log the
> failed CDR posting and move on to the next." Our outbound dispatcher
> (`internal/outbound`) implements this bounded retry + drop policy.

### 2.1 Inbound authentication strategies

The handler at `internal/pms/tigertms/tigertms.go` reads the URL
token, hashes it (SHA-256), and looks it up against
`tenant_inbound_tokens` (see [`tigertms-cloud-backend.md`](tigertms-cloud-backend.md)
for schema + admin API). Three strategies are supported per token,
configured in the `auth_strategy` column:

| Strategy | What gets checked | When to use |
|---|---|---|
| `url_token` (default) | URL token IS the only auth. Looked up by hash; nothing in headers. | iLink-on-prem setups where the URL is hidden behind firewall + TLS |
| `bearer` | URL token identifies the tenant, plus `Authorization: Bearer <secret>`. Both must match. | iLink cloud or any sender that needs header-based auth |
| `basic` | URL token identifies the tenant, plus `Authorization: Basic <base64(user:pass)>`. Both must match. | Legacy senders that only support basic auth |

Plaintext tokens/secrets are never persisted — only their SHA-256
hex digests. Bearer / basic secrets are echoed back exactly once on
the `POST /admin/tenants/{id}/tokens` response so the operator can
configure the upstream sender. URL tokens (the long random secret)
are also echoed back once on create.

Token rotation: create a new token via the admin API, deploy to the
sender, then DELETE the old one. Tokens are never deleted, only
disabled (`enabled=false`), so audit trails survive rotation.

---

## 3. Endpoints (iLink → us)

All endpoints accept a `siteid` header for backwards compat with the
PDF examples. The token in the URL path is the actual multi-tenant
discriminator we use in production (see §2.1). Body is a single JSON
object whose top-level key varies by endpoint (`setguest`,
`classofservice`, `messagewaiting`, etc.).

### 3.1 `POST /API/setguest`

Combined check-in / check-out + name update. **The iLink server
expects us to clear MWI, DDI, and DND when status transitions to
`vacant` — this is iLink's documented behavior, not ours to enforce:**

> "If the occupied Status for the Extension goes to 'vacant' then any
> MWI, DDI, DND settings should be routinely cleared."

**Body** (top-level key: `setguest`):

```json
{
  "extn": "4100",
  "status": "occupied",
  "title": "Mr",
  "firstname": "John",
  "lastname": "Smith",
  "language": "english",
  "group": "BAE",
  "vip": "1"
}
```

On checkout (status `vacant`):

```json
{
  "extn": "4100",
  "status": "vacant",
  "title": "",
  "firstname": "4100",
  "lastname": "vacant",
  "language": "",
  "group": "",
  "vip": ""
}
```

| Field | Type | Notes |
|---|---|---|
| `extn` | string | Extension number. **Not** room number. |
| `status` | `occupied` / `vacant` | Single field drives check-in/out |
| `title` | string | e.g. `Mr`, `Mrs`, `Dr` |
| `firstname` | string | Guest first name |
| `lastname` | string | Guest last name |
| `language` | string | ISO-style code, e.g. `english` |
| `group` | string | Booking group / corporate code |
| `vip` | `1` / `0` / empty | VIP flag |

### 3.2 `POST /API/setcos`

**Body** (top-level key: `classofservice`):

```json
{"extn":"4100","cos":"2"}
```

COS levels: `0` = barred, `1` = local, `2` = national, `3` = no
restrictions. iLink notes: "we tend to only use barred / no
restrictions (or whatever the hotel have in place for the no
restrictions)".

### 3.3 `POST /API/setmw`

**Body** (top-level key: `messagewaiting`):

```json
{"extn":"4100","mw":"on"}
```

`mw` values: `on` / `off`. iLink notes: "if Innline is being used,
this may be achieved via SipNotify" — i.e. the iLink server may
expect SIP NOTIFY in addition to (or instead of) the lamp state we
control.

### 3.4 `POST /API/setsipdata`

**Body** (top-level key: `sipdata`):

```json
{"extn":"4100","sippassword":"Smith1234"}
```

iLink uses this for **iConnect BYOD** (Bring Your Own Device) — when a
guest registers their mobile as an extension. iLink allocates 3-4 extra
extensions per room for BYOD and rotates the SIP password on
check-in/out.

### 3.5 `POST /API/setddi`

**Body** (top-level key: `ddiinformation`):

```json
{"extn":"4100","ddi":"5543","operation":"set"}
{"extn":"4100","ddi":"","operation":"clear"}
```

`operation`: `set` or `clear`.

### 3.6 `POST /API/setdnd`

**Body** (top-level key: `dnd`):

```json
{"extn":"4100","dnd":"on"}
```

`dnd` values: `on` / `off`.

### 3.7 `POST /API/setwakeup`

**Body** (top-level key: `wakeup`):

```json
{"extn":"4100","action":"set","wakeuptime":"24-08-2017 08:00:00"}
{"extn":"4100","action":"clear","wakeuptime":"24-08-2017 08:00:00"}
{"extn":"4100","action":"clearall","wakeuptime":""}
```

| Field | Notes |
|---|---|
| `extn` | Extension number |
| `action` | `set` / `clear` / `clearall` |
| `wakeuptime` | Format: `dd-mm-yyyy hh:mm:ss`. **Blank** for `clearall` — iLink expects ALL scheduled wake-ups for that extension to be cancelled |

iLink notes: "Wakeup can be Set on the PMS or by the Connected Guests
iCharge System." — i.e. wake-up events can originate from the PMS
**or** from the guest's own device via iCharge. We should treat both
as authoritative.

---

## 4. CDR endpoint (us → TigerTMS)

**`POST /API/CDR`** — body (top-level key: `message`) is a JSON
object whose field names mirror Asterisk CDR fields 1:1.

```json
{
  "ccsrc": "",
  "clid": "821<821>",
  "src": "821",
  "dst": "630",
  "calleenum": "630",
  "dcontext": "DLPN_DialPlan1",
  "channel": "SIP/821-0000002",
  "dstchannel": "",
  "lastapp": "Hangup",
  "lastdata": "",
  "start": "2017-05-17 16:51:42",
  "answer": "2017-05-17 16:51:42",
  "end": "2017-05-17 16:51:42",
  "duration": "0",
  "billsec": "0",
  "disposition": "8",
  "amaflags": "3",
  "accountcode": "",
  "calltype": "",
  "uniqueid": "149501102.5"
}
```

| Field | Type | Description |
|---|---|---|
| `accountcode` | string(20) | Account code, Party A |
| `src` | string(80) | Caller ID number (Party A) |
| `dst` | string(80) | Destination extension (Party B) |
| `dcontext` | string(80) | Destination context |
| `clid` | string(80) | Caller ID with text |
| `channel` | string(80) | Party A channel name (e.g. `SIP/821-0000002`) |
| `dstchannel` | string(80) | Party B channel name |
| `lastapp` | string(80) | Last application executed |
| `lastdata` | string(80) | App data for last application |
| `start` | date/time | CDR created |
| `answer` | date/time | Party A answered |
| `end` | date/time | CDR finished |
| `duration` | int | Seconds, start→end |
| `billsec` | int | Seconds, answer→end |
| `disposition` | enum | See Asterisk CDR dispositions |
| `amaflags` | enum | AMA flags |
| `uniqueid` | string(32) | Party A unique ID |
| `calleenum` | string | **TigerTMS extension** of the Asterisk CDR `dst` field |
| `calltype` | string | **TigerTMS extension** — free-form (e.g. `internal` / `external`) |
| `ccsrc` | string | Optional |

**Response**: HTTP 200 with body
`{"response":"RECEIVEDOK"}` on success, `{"response":"ERROR"}` on
failure. **Different shape from the inbound success/failed format.**

**Retry contract** (from PDF):

- HTTP 401, network error, or non-`{"response":"RECEIVEDOK"}` →
  buffer locally and retry when the link re-establishes.
- "Try 3 times then dump and log the failed CDR posting and move on
  to the next."

---

## 5. Mapping to our internal model

| iLink field | Our internal field | Notes |
|---|---|---|
| `extn` | `evt.Room` | **iLink sends the extension, not the room.** Our `RoomMapper` already supports extension↔room lookup, but the route through the mapper needs to reverse direction. |
| `status: occupied` | `pms.EventCheckIn` | |
| `status: vacant` | `pms.EventCheckOut` + auto-clear MWI/DDI/DND | iLink spec says clear on vacant; our `handleCheckOut` already does this on Bicom |
| `firstname + lastname` | `evt.GuestName = "{firstname} {lastname}"` | |
| `language` | `evt.Metadata["language"]` | New metadata key |
| `group` | `evt.Metadata["group"]` | New metadata key |
| `vip` | `evt.Metadata["vip"]` | New metadata key |
| `cos` | `pms.EventRoomStatus` with `evt.Metadata["class_of_service"]` | Emitted on the event but no PBX-side consumer yet (Tier E follow-on — needs `pbxware.ext.edit service_plan=…` on Bicom) |
| `mw` | `pms.EventMessageWaiting` | |
| `sippassword` | `pms.EventRoomStatus` with `evt.Metadata["sip_password"]` | iConnect BYOD — no PBX consumer yet (Bicom `pbxware.ext.edit` with a password field — needs feature work) |
| `ddi` + `operation` | `pms.EventRoomStatus` with `evt.Metadata["ddi"]`, `evt.Metadata["ddi_op"]` | No consumer — see Tier E |
| `dnd` | `pms.EventDND` | |
| `wakeuptime` (full datetime) | `evt.Metadata["wakeup_time_full"]` (e.g. `24-08-2017 08:00:00`) | Preserved verbatim on the event. `tenant.handleWakeUp` parses via `parseWakeUpTimeFull` (`dd-mm-yyyy hh:mm:ss` per the PDF; ISO-8601 and `hh:mm`-without-seconds also accepted). |
| `action: clearall` | Cancel all `wakeup_calls` rows for `extn` | New behavior; no per-room clear-all logic today |

---

## 6. iCharge (guest self-service)

iLink mentions iCharge as a **separate** iLink-connected system that
lets guests self-serve (set wake-up, view minibar, etc.) from the room
phone or app. Events from iCharge flow through iLink using the same
wire format above — i.e. an iCharge wake-up arrives as a normal
`/API/setwakeup`. Our handler should not need to differentiate source
(PMS vs. iCharge), but we should preserve the source in metadata for
audit (`evt.Metadata["source"]` ∈ `{pms, icharge, tigertms_cloud}`).

---

## 7. What this doc doesn't cover

The PDFs describe the **iLink** (Asterisk) protocol. The user has
asked us to additionally support a **TigerTMS cloud backend** flow,
where TigerTMS cloud (vendor SaaS) calls us on behalf of iLink
installations. TigerTMS does not publish that protocol in either of
the included PDFs — see [gaps doc](./tigertms-cloud-backend.md#open-questions--unresolved-gaps).