CREATE TABLE telegram_destinations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES tenant_channels(id) ON DELETE CASCADE,
    telegram_chat_id VARCHAR(255) NOT NULL,
    title VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type IN ('group', 'supergroup', 'channel')),
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    source VARCHAR(50) NOT NULL DEFAULT 'webhook' CHECK (source IN ('webhook', 'manual')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_telegram_destinations_unique_chat
ON telegram_destinations(tenant_id, channel_id, telegram_chat_id);

ALTER TABLE campaigns
ADD COLUMN selected_telegram_destination_ids JSONB NOT NULL DEFAULT '[]';

ALTER TABLE campaign_deliveries
ALTER COLUMN contact_id DROP NOT NULL;

ALTER TABLE campaign_deliveries
ADD COLUMN target_type VARCHAR(50) NOT NULL DEFAULT 'contact'
CHECK (target_type IN ('contact', 'telegram_destination'));
