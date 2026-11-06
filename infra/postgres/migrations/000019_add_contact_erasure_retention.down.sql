DROP INDEX IF EXISTS idx_contacts_delete_after;
ALTER TABLE contacts DROP COLUMN IF EXISTS delete_after_at;
ALTER TABLE contacts DROP COLUMN IF EXISTS anonymized_at;
