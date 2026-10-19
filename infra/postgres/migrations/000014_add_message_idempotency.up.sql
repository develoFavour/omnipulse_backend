-- Prevent retried provider webhooks from creating duplicate inbox messages.
CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_tenant_platform_id
    ON messages (tenant_id, platform_message_id)
    WHERE platform_message_id IS NOT NULL;
