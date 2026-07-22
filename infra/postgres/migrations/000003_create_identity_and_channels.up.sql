-- =========================================================================
-- 1. MULTI-TENANT WORKSPACE & USER IDENTITY LAYERS
-- =========================================================================

-- Create Core Workspaces Container Table
CREATE TABLE tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_name VARCHAR(255) NOT NULL,
    onboarding_completed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create Users Profile Table (Tied directly to Clerk External Identity IDs)
CREATE TABLE users (
    id VARCHAR(255) PRIMARY KEY, -- Accommodates Clerk's string format (e.g., 'user_2NizX9...')
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL UNIQUE,
    role VARCHAR(50) NOT NULL DEFAULT 'member', -- 'admin', 'member'
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- =========================================================================
-- 2. OMNICHANNEL CREDENTIALS CONTAINER
-- =========================================================================

-- Create Dynamic Credentials Table for Connected Chat Ecosystems
CREATE TABLE tenant_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    platform_name VARCHAR(50) NOT NULL CHECK (platform_name IN ('whatsapp', 'telegram', 'instagram', 'x')),
    sender_identity VARCHAR(255) NOT NULL, -- Public handle (e.g., store number or @BotUsername)
    encrypted_credentials JSONB NOT NULL,   -- Flexibly stores custom authentication metadata matrices
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending', 'suspended')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Guardrail: Restrict each workspace to a single active configuration per platform type
CREATE UNIQUE INDEX idx_tenant_platform_unique ON tenant_channels(tenant_id, platform_name);

-- =========================================================================
-- 3. THE RE-ARCHITECTED CONTACTS DATA LEDGER
-- =========================================================================

-- Drop the old simple Phase 1 contacts table to avoid column mismatch errors
DROP TABLE IF EXISTS contacts CASCADE;

-- Rebuild the multi-tenant Audience Directory registry container
CREATE TABLE contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100),
    channel VARCHAR(50) NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'instagram', 'x')),
    routing_value VARCHAR(255) NOT NULL, -- Stores direct target numbers or handle details
    source VARCHAR(50) NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'csv_import', 'inbound_webhook')),
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'opted_out')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Security Guardrail: Prevent workspaces from creating duplicate channel routings
CREATE UNIQUE INDEX idx_tenant_contact_routing ON contacts(tenant_id, channel, routing_value);

-- =========================================================================
-- 4. THE CROSS-POSTING CAMPAIGN ENGINE UPGRADES
-- =========================================================================

-- Setup strong enum definitions tracking broadcast modes
CREATE TYPE campaign_delivery_type AS ENUM ('direct_message', 'public_post');

-- Extend the existing campaigns framework container with multi-tenant vectors
ALTER TABLE campaigns ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE;
ALTER TABLE campaigns ADD COLUMN delivery_type campaign_delivery_type NOT NULL DEFAULT 'direct_message';
ALTER TABLE campaigns ADD COLUMN selected_channels JSONB NOT NULL DEFAULT '[]'; -- Targeted social arrays