-- Recipient erasure: deleting a recipient anonymizes the row immediately
-- (PII blanked, status opted_out) and the retention sweep hard-deletes it
-- once delete_after_at (anonymized_at + 30 days) passes. Delivery rows are
-- unlinked, not cascaded, so campaign reports keep their history.

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS anonymized_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS delete_after_at TIMESTAMP WITH TIME ZONE;

CREATE INDEX IF NOT EXISTS idx_contacts_delete_after ON contacts(delete_after_at) WHERE delete_after_at IS NOT NULL;

-- Older databases carried NOT NULL here; the sweep NULLs contact_id on
-- hard-delete to preserve delivery history.
ALTER TABLE campaign_deliveries ALTER COLUMN contact_id DROP NOT NULL;
