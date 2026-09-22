-- 000008_add_campaign_scheduler.up.sql

ALTER TYPE campaign_status ADD VALUE IF NOT EXISTS 'scheduled';

ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX IF NOT EXISTS idx_campaigns_scheduled ON campaigns(status, scheduled_at) WHERE status = 'scheduled';
