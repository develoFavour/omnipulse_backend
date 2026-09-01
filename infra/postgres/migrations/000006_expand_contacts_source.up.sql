-- Migration: Expand the contacts.source CHECK constraint to support WhatsApp sync sources
ALTER TABLE contacts DROP CONSTRAINT IF EXISTS contacts_source_check;
ALTER TABLE contacts ADD CONSTRAINT contacts_source_check
    CHECK (source IN ('manual', 'csv_import', 'inbound_webhook', 'whatsapp_sync', 'whatsapp_inbound', 'whatsapp_group_sync'));
