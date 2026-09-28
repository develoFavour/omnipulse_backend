-- 000010_add_notifications.up.sql
-- Real notification events for the agency bell icon

CREATE TYPE notification_type AS ENUM (
    'campaign_completed',
    'delivery_failure',
    'new_opt_out',
    'contact_import_finished',
    'channel_disconnected'
);

CREATE TABLE IF NOT EXISTS notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    type        notification_type NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}',
    is_read     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_tenant_read
    ON notifications(tenant_id, is_read, created_at DESC);
