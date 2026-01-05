# PMS Protocol Reference

Quick reference for Property Management System protocols supported by the Bicom Hospitality Integration.

---

## Mitel SX-200 / MiVoice Protocol

### Framing

| Control | Hex | ASCII | Description |
|---------|-----|-------|-------------|
| STX | `0x02` | `^B` | Start of message |
| ETX | `0x03` | `^C` | End of message |
| ENQ | `0x05` | `^E` | Enquiry (polling) |
| ACK | `0x06` | `^F` | Acknowledged |
| NAK | `0x15` | `^U` | Not acknowledged |

### Message Format

```
<STX><FUNC><STATUS><ROOM_#><ETX>
     │     │       └─ 5 chars, space-padded
     │     └─ 1-2 chars
     └─ 2-3 chars
```

Total payload: 10 characters (after STX, before ETX)

### Function Codes

| Code | Function | Status | Example |
|------|----------|--------|---------|
| `CHK` | Check-In/Out | `1`=in, `0`=out | `CHK1 2129` |
| `MW ` | Message Waiting | `1`=on, `0`=off | `MW 1 2129` |
| `NAM` | Guest Name | `1`=set | `NAM1 2129` |
| `RM ` | Room Status | `1`=occupied | `RM 1 2129` |
| `DND` | Do Not Disturb | `1`=on, `0`=off | `DND1 2129` |

### Examples

**Check-In Room 2129:**
```
Raw:    02 43 48 4B 31 20 32 31 32 39 03
Parsed: <STX>CHK1 2129<ETX>
```

**Message Waiting ON for Room 101:**
```
Raw:    02 4D 57 20 31 20 20 31 30 31 03
Parsed: <STX>MW 1   101<ETX>
```

---

## Oracle FIAS / Fidelio Protocol

### Transport

- TCP/IP connection to FIAS server
- Persistent connection with Link Record handshake
- ASCII text records with field delimiters

### Record Format

```
<RECORD_TYPE>|<FIELD>=<VALUE>|<FIELD>=<VALUE>|...|
```

### Common Record Types

| Type | Description | Direction |
|------|-------------|-----------|
| `LR` | Link Record (handshake) | Bidirectional |
| `GI` | Guest Check-In | PMS → PBX |
| `GO` | Guest Check-Out | PMS → PBX |
| `MW` | Message Waiting | PMS → PBX |
| `RS` | Room Status | PBX → PMS |
| `WK` | Wake-Up Call | PMS → PBX |

### Common Field Types

| Field | Description | Example |
|-------|-------------|---------|
| `RN` | Room Number | `RN1015` |
| `GN` | Guest Name | `GNSmith, John` |
| `DA` | Date (YYMMDD) | `DA260102` |
| `TI` | Time (HHMM) | `TI1430` |
| `FL` | Flag | `FL1` |
| `RI` | Reservation ID | `RI12345` |

### Examples

**Link Record (capabilities negotiation):**
```
LR|DA|TI|RN|GN|FL|RI|
```

**Guest Check-In:**
```
GI|RN1015|GNSmith, John|DA260102|TI1430|RI12345|
```

**Guest Check-Out:**
```
GO|RN1015|DA260102|TI1100|
```

**Message Waiting Indicator:**
```
MW|RN1015|FL1|
```

---

## TigerTMS iLink REST API

TigerTMS iLink is middleware that translates between PMS systems and PBX via HTTP REST API.

### Transport

- HTTP/HTTPS REST API
- TigerTMS pushes events to our endpoints
- Query parameters or JSON body format

### Endpoints

| Endpoint | Description |
|----------|-------------|
| `/API/setguest` | Guest check-in/out |
| `/API/setcos` | Class of Service |
| `/API/setmw` | Message Waiting |
| `/API/setsipdata` | SIP extension data |
| `/API/setddi` | DDI/DID assignment |
| `/API/setdnd` | Do Not Disturb |
| `/API/setwakeup` | Wake-up calls |
| `/API/CDR` | Call Detail Records |

### Examples

**Guest Check-In:**
```
POST /API/setguest?room=2129&checkin=true&guest=Smith%2C+John
```

**Message Waiting ON:**
```
POST /API/setmw?room=2129&mw=true
```

See [TigerTMS Integration Guide](tigertms.md) for full documentation.

---

## Protocol Comparison

| Feature | Mitel SX-200 | FIAS | TigerTMS |
|---------|--------------|------|----------|
| Transport | Serial/Telnet | TCP/IP | HTTP REST |
| Encoding | Fixed-width ASCII | Pipe-delimited | Query params/JSON |
| Framing | STX/ETX control chars | Record-based | HTTP request |
| Handshake | ENQ/ACK polling | Link Record | Auth header |
| Complexity | Simple | Feature-rich | Modern |
| Our Role | Socket listener | Socket listener | HTTP server |
| Typical PMS | Legacy systems | OPERA, Suite8 | Any via middleware |
