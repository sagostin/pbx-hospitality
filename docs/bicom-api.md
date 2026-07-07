# Bicom PBXware API Reference

This document describes the Bicom PBXware REST API integration used by the hospitality service.

## Authentication

The Bicom API uses API keys for authentication. Keys are created in:
**PBXware Admin → Admin Settings → API Keys**

A "Master Key" with all permissions is provided by default.

## API Structure

All requests use this format:
```
https://{pbx-host}/api/?key={api_key}&action={action}&format=json&{params}
```

**Action format:** `application.object.method`

---

## Extension Management

### List Extensions
```
GET /api/?action=pbxware.ext.list&server={tenant_id}
```

### Get Extension Details
```
GET /api/?action=pbxware.ext.configuration&id={ext_id}
```

### Update Extension Name
```
POST /api/?action=pbxware.ext.edit&id={ext_id}&name={guest_name}
```

### Update Service Plan
```
POST /api/?action=pbxware.ext.edit&id={ext_id}&service_plan={plan_id}
```

---

## Wake-Up Calls

The Bicom PBXware REST API exposes wake-up as a **state toggle** — the API
confirms whether an extension has a wake-up scheduled but **does not accept a
time parameter**. The actual ring-at-HH:MM is fired by the PBX itself (the
guest sets the time via the room phone's `*411` feature code, or the time
is configured via the PBXware UI / dialplan).

For hospitality integrations where the PMS drives the wake-up time
programmatically, the service complements the state toggle with an in-process
**WakeUpScheduler** that uses ARI `Channels.Originate` to place the call at
the scheduled time (Tier 1 in `ROADMAP.md`).

### Operator-set wake-up (the variant hospitality uses)

```
POST /api/?action=pbxware.ext.es.opwakeupcall.set&server={tenant_id}&id={ext_id}&state=yes
POST /api/?action=pbxware.ext.es.opwakeupcall.set&server={tenant_id}&id={ext_id}&state=no
GET  /api/?action=pbxware.ext.es.opwakeupcall.get&server={tenant_id}&id={ext_id}
```

Accepted `state` values: `yes`, `no`, `1`, `0`.

### Self-set wake-up (guest dialed *411 themselves)

```
POST /api/?action=pbxware.ext.es.wakeupcall.set&server={tenant_id}&id={ext_id}&state=yes
POST /api/?action=pbxware.ext.es.wakeupcall.set&server={tenant_id}&id={ext_id}&state=no
GET  /api/?action=pbxware.ext.es.wakeupcall.get&server={tenant_id}&id={ext_id}
```

### Bulk (set every Enhanced Service flag in one call)

```
POST /api/?action=pbxware.ext.es.states.set&server={tenant_id}&id={ext_id}&all=yes&callerid=&dnd=&wakeupcall=&opwakeupcall=&...
```

### Legacy endpoint

> **Note:** The earlier docs shipped with this repository referenced
> `pbxware.ext.es.wakeupcall.edit` with a `time` parameter. That endpoint
> does not exist in the public Bicom API. Use `opwakeupcall.set` instead
> (with the WakeUpScheduler doing the time-based originate).

> Wake-up calls can also be managed via feature codes:
> - `*411` - User sets own wake-up call
> - `*412` - Operator sets wake-up for any extension

---

## Voicemail

### Delete All Messages
```
POST /api/?action=pbxware.vm.delete_all&id={ext_id}
```

### Get Message Count
```
GET /api/?action=pbxware.vm.count&id={ext_id}
```

**Response:**
```json
{"new": 2, "old": 5}
```

### Set Voicemail Greeting Type
```
POST /api/?action=pbxware.ext.es.vm.edit&id={ext_id}&greeting={type}
```

**Greeting Types:**
- `default` - System default greeting (reset on checkout)
- `unavailable` - Custom unavailable greeting
- `busy` - Custom busy greeting
- `none` - No greeting (straight to beep)

> **Hospitality Use:** On guest checkout, reset greeting to `default` to remove any personalized recording.

---

## Enhanced Services

### Do Not Disturb
```
POST /api/?action=pbxware.ext.es.dnd.edit&id={ext_id}&enabled={0|1}
```

### Call Forward
```
POST /api/?action=pbxware.ext.es.callforward.edit&id={ext_id}&enabled={0|1}&destination={number}
```

---

## Service Plans

### List Available Plans
```
GET /api/?action=pbxware.sp.list
```

---

## Response Format

All responses follow this structure:
```json
{
  "success": true,
  "message": "Operation completed",
  "data": { ... }
}
```

## Error Handling

On error, `success` is `false`:
```json
{
  "success": false,
  "message": "Extension not found"
}
```

---

## Configuration

Add these settings to your tenant configuration:

```yaml
pbx:
  api_url: "https://pbx.example.com"  # PBXware base URL
  api_key: "${PBX_API_KEY}"           # From Admin Settings
  tenant_id: "1"                       # Server/tenant ID
```

Generate an API key in PBXware:
1. Go to **Admin Settings → API Keys**
2. Click **Add API Key**
3. Set permissions as needed
4. Copy the generated key
