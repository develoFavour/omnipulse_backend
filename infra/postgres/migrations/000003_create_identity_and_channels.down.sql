-- 1. Strip out the custom campaign engine additions
ALTER TABLE campaigns DROP COLUMN IF EXISTS selected_channels;
ALTER TABLE campaigns DROP COLUMN IF EXISTS delivery_type;
ALTER TABLE campaigns DROP COLUMN IF EXISTS tenant_id;
DROP TYPE IF EXISTS campaign_delivery_type;

-- 2. Drop the multi-tenant contacts registry
DROP INDEX IF EXISTS idx_tenant_contact_routing;
DROP TABLE IF EXISTS contacts CASCADE;

-- 3. Drop the channels engine and its protection indexes
DROP INDEX IF EXISTS idx_tenant_platform_unique;
DROP TABLE IF EXISTS tenant_channels CASCADE;

-- 4. Drop the identity workspace tables entirely
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS tenants CASCADE;

-- 5. Re-provision the structural fallback baseline Phase 1 Contacts framework for safety
CREATE TABLE contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100),
    channel VARCHAR(50) NOT NULL,
    routing_value VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);