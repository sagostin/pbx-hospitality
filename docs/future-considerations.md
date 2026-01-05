# Future Considerations

This document outlines features and enhancements for future development phases.

---

## Security & Access Control

### IP Whitelisting / ACLs

For connecting to external port-forwarded PMS servers:

```yaml
tenants:
  - id: hotel-alpha
    pms:
      host: pms.customer.com
      port: 3722
      # Future: Bind outbound connections to specific source IP
      source_ip: "203.0.113.10"
      # Future: Allowed destination IPs (validation)
      allowed_ips:
        - "198.51.100.0/24"
```

**Use Cases:**
- Customer firewall rules require known egress IPs
- Validate PMS server hasn't moved unexpectedly
- Multi-homed servers with specific routing needs

### TLS / mTLS Support

For secure PMS connections:

```yaml
pms:
  tls:
    enabled: true
    cert_file: /certs/client.crt
    key_file: /certs/client.key
    ca_file: /certs/ca.crt
    skip_verify: false  # Never in production
```

---

## Cloud PMS Integration

### Customer Identification Methods

For cloud-based PMS systems that serve multiple customers:

| Method | Config | Description |
|--------|--------|-------------|
| **API Token** | `auth_token` | Bearer token in requests |
| **API Key Header** | `api_key` | Custom header authentication |
| **TLS Client Cert** | `tls.cert_file` | mTLS identity |
| **Customer ID Header** | `customer_id` | `X-Customer-ID` header |
| **Static Egress IP** | (infrastructure) | Customer whitelists our IP |

```yaml
pms:
  protocol: fias-cloud
  host: pms.cloudvendor.com
  port: 443
  auth:
    type: bearer
    token: "${PMS_API_TOKEN}"
  customer_id: "hotel-alpha-12345"
```

### Multi-Region Deployment

```yaml
regions:
  - id: us-east
    egress_ip: "203.0.113.10"
    tenants: ["hotel-east-1", "hotel-east-2"]
  - id: eu-west
    egress_ip: "198.51.100.20"
    tenants: ["hotel-europe-1"]
```

---

## Reliability Enhancements

### Connection Retry with Backoff

```yaml
pms:
  retry:
    max_attempts: 10
    initial_delay: 1s
    max_delay: 5m
    backoff_factor: 2.0
```

### Health Monitoring

- **Per-tenant health status** in `/health` endpoint
- **Alerting integration** via webhook
- **Circuit breaker** for failing PMS connections

### Failover

```yaml
pms:
  primary:
    host: pms.primary.com
    port: 3722
  failover:
    host: pms.backup.com
    port: 3722
  failover_threshold: 3  # failures before switch
```

---

## Compliance & Audit

### Data Retention Policies

```yaml
retention:
  pms_events: 90d      # Keep event logs for 90 days
  guest_sessions: 1y   # Keep session history for 1 year
  audit_logs: 7y       # GDPR/compliance retention
```

### PII Handling

- **Guest name masking** in logs (configurable)
- **Data export** for GDPR requests
- **Automated purge** after retention period

---

## Additional Protocol Support

### Future PMS Protocols

| Protocol | Vendor | Status |
|----------|--------|--------|
| **TigerTMS iLink** | TigerTMS | Documented |
| **HTNG** | Generic hospitality | Planned |
| **Hilton PEP** | Hilton | On request |
| **Hyatt HIS** | Hyatt | On request |
| **Marriott FOSSE** | Marriott | On request |
| **FIAS over HTTPS** | Cloud vendors | Planned |

### Wake-Up Call Enhancements

- **IVR confirmation** - Guest presses 1 to confirm wake-up
- **Retry logic** - Multiple attempts with escalation
- **PMS callback** - Notify PMS of wake-up completion/failure

### Zultys Wake-Up Call Scheduler

Zultys does not provide a native wake-up call API. For Zultys deployments requiring wake-up calls:

| Component | Description |
|-----------|-------------|
| **Call Scheduler** | PostgreSQL table storing scheduled wake-up times |
| **FreeSWITCH Originator** | Cron-triggered service that originates calls at scheduled times |
| **Wake-Up IVR** | FreeSWITCH dialplan plays announcement, optionally waits for confirmation |
| **Result Callback** | Reports success/failure back to hospitality service |

```
┌──────────────┐     ┌───────────────┐     ┌─────────────┐
│ Hospitality  │────▶│  PostgreSQL   │────▶│ FreeSWITCH  │
│ Service      │     │  (scheduler)  │     │ Originator  │
└──────────────┘     └───────────────┘     └──────┬──────┘
                                                   │
                                                   ▼
                                           ┌─────────────┐
                                           │   Zultys    │
                                           │   (SIP)     │
                                           └─────────────┘
```

---

## API Enhancements

### Webhook Notifications

```yaml
webhooks:
  - url: https://customer.com/hospitality/events
    secret: "${WEBHOOK_SECRET}"
    events: ["checkin", "checkout", "wakeup_completed"]
```

### Rate Limiting

```yaml
api:
  rate_limit:
    requests_per_minute: 100
    burst: 20
```

### API Authentication

```yaml
api:
  auth:
    type: api_key  # or "oauth2", "basic"
    keys:
      - id: admin-key
        secret: "${ADMIN_API_KEY}"
        scopes: ["read", "write", "admin"]
```

---

## Scalability

### Horizontal Scaling

- **Tenant sharding** - Distribute tenants across instances
- **Leader election** - Only one instance handles each PMS connection
- **Redis coordination** - For distributed state

### Database Optimization

- **Connection pooling tuning** - Per-load adjustment
- **Read replicas** - For reporting/events queries
- **Partitioning** - `pms_events` table by date

---

## Monitoring Enhancements

### Grafana Dashboard

- PMS connection status per tenant
- Event processing rate and latency
- Error rates by type
- Guest session counts

### Alerting Rules

| Alert | Condition | Severity |
|-------|-----------|----------|
| PMS Disconnected | `pms_connection_status == 0` for 5m | Critical |
| High Error Rate | `rate(errors) > 10/min` | Warning |
| Event Backlog | Unprocessed events > 100 | Warning |

---

## Implementation Priority

| Feature | Priority | Effort | Notes |
|---------|----------|--------|-------|
| TigerTMS Implementation | High | Medium | Documentation complete |
| IP Whitelisting | Medium | Low | Customer request driven |
| Cloud PMS Auth | High | Medium | Needed for SaaS vendors |
| TLS/mTLS | High | Medium | Security requirement |
| Retry with Backoff | High | Low | Reliability improvement |
| Webhooks | Medium | Medium | Integration feature |
| Additional Protocols | Low | High | Per-customer need |
