DROP INDEX IF EXISTS uq_active_whatsapp_account_identity;

ALTER TABLE tenant_channels
    DROP COLUMN IF EXISTS account_identity;
