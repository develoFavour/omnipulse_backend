-- 000009_index_contacts_for_search.down.sql

DROP INDEX IF EXISTS idx_contacts_tenant_created;
DROP INDEX IF EXISTS idx_contacts_tenant_channel;
DROP INDEX IF EXISTS idx_contacts_tenant_name;
DROP INDEX IF EXISTS idx_contacts_tenant_routing;
