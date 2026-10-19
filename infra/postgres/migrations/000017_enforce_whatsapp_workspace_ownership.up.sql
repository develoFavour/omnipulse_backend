-- Keep one active WhatsApp account attached to one workspace globally.
-- The existing sender identity contains the phone number for both QR and
-- Meta connections, so use a normalized phone-based ownership key when
-- backfilling legacy rows.
ALTER TABLE tenant_channels
    ADD COLUMN IF NOT EXISTS account_identity VARCHAR(255);

UPDATE tenant_channels
SET account_identity = 'phone:' || NULLIF(regexp_replace(sender_identity, '[^0-9]', '', 'g'), '')
WHERE platform_name = 'whatsapp'
  AND account_identity IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_active_whatsapp_account_identity
    ON tenant_channels(account_identity)
    WHERE platform_name = 'whatsapp'
      AND status = 'active'
      AND account_identity IS NOT NULL;
