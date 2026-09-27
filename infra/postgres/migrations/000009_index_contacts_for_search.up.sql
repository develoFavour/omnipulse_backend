-- 000009_index_contacts_for_search.up.sql

CREATE INDEX IF NOT EXISTS idx_contacts_tenant_created ON contacts(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_contacts_tenant_channel ON contacts(tenant_id, channel);
CREATE INDEX IF NOT EXISTS idx_contacts_tenant_name ON contacts(tenant_id, first_name);
CREATE INDEX IF NOT EXISTS idx_contacts_tenant_routing ON contacts(tenant_id, routing_value);
