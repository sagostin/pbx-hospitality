-- =============================================================================
-- pbx-hospitality: wakeup_calls table (Tier 1)
-- =============================================================================
-- Used by the WakeUpScheduler in internal/wakeup/scheduler.go.
-- Rows are inserted by tenant.handleWakeUp after a successful
-- ScheduleWakeUpCall on the PBX; the scheduler fires them via
-- pbx.Provider.OriginateWakeUp at scheduled_at.
-- =============================================================================

CREATE TABLE IF NOT EXISTS wakeup_calls (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       VARCHAR(64) NOT NULL,
    extension       VARCHAR(32) NOT NULL,
    scheduled_at    TIMESTAMPTZ NOT NULL,
    status          VARCHAR(16) NOT NULL DEFAULT 'pending',  -- pending|originated|completed|failed|cancelled
    attempt_count   INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    originated_at   TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    metadata        JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_wakeup_tenant
    ON wakeup_calls(tenant_id);

-- Partial index that backs the scheduler's hot path:
--   SELECT * FROM wakeup_calls WHERE status = 'pending' AND scheduled_at <= NOW()
-- Only pending rows are visited; the partial index keeps it tiny.
CREATE INDEX IF NOT EXISTS idx_wakeup_due
    ON wakeup_calls(scheduled_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_wakeup_status
    ON wakeup_calls(status)
    WHERE status IN ('pending', 'originated');