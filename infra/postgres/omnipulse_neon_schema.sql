-- =========================================================================
-- OMNIPULSE PRODUCTION DATABASE SCHEMA (NEON POSTGRESQL)
-- =========================================================================

-- 1. EXTENSIONS & ENUMS
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE platform_type AS ENUM ('whatsapp', 'telegram', 'x', 'instagram');
CREATE TYPE campaign_status AS ENUM ('draft', 'pending', 'processing', 'completed', 'failed');
CREATE TYPE dispatch_status AS ENUM ('queued', 'in_flight', 'rate_limited', 'delivered', 'failed');
CREATE TYPE campaign_delivery_type AS ENUM ('direct_message', 'public_post');
CREATE TYPE delivery_status_enum AS ENUM ('sent', 'delivered', 'failed');

CREATE TABLE IF NOT EXISTS tenants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_name VARCHAR(255) NOT NULL,
    onboarding_completed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(255) PRIMARY KEY, 
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL UNIQUE,
    role VARCHAR(50) NOT NULL DEFAULT 'member', 
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);


CREATE TABLE IF NOT EXISTS tenant_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    platform_name VARCHAR(50) NOT NULL CHECK (platform_name IN ('whatsapp', 'telegram', 'instagram', 'x')),
    sender_identity VARCHAR(255) NOT NULL,
    encrypted_credentials JSONB NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending', 'suspended')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_platform_unique ON tenant_channels(tenant_id, platform_name);


CREATE TABLE IF NOT EXISTS contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100),
    channel VARCHAR(50) NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'instagram', 'x')),
    routing_value VARCHAR(255) NOT NULL,
    source VARCHAR(50) NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'csv_import', 'inbound_webhook')),
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'opted_out')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_contact_routing ON contacts(tenant_id, channel, routing_value);

-- 6. TELEGRAM DESTINATIONS (Groups & Supergroups)
CREATE TABLE IF NOT EXISTS telegram_destinations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES tenant_channels(id) ON DELETE CASCADE,
    telegram_chat_id VARCHAR(255) NOT NULL,
    title VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL CHECK (type IN ('group', 'supergroup', 'channel')),
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    source VARCHAR(50) NOT NULL DEFAULT 'webhook' CHECK (source IN ('webhook', 'manual')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_telegram_destinations_unique_chat
ON telegram_destinations(tenant_id, channel_id, telegram_chat_id);

-- 7. CAMPAIGNS (Cross-Posting Broadcast Engine)
CREATE TABLE IF NOT EXISTS campaigns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    message_body TEXT NOT NULL,
    external_template_code VARCHAR(100),
    media_url TEXT,
    delivery_type campaign_delivery_type NOT NULL DEFAULT 'direct_message',
    selected_channels JSONB NOT NULL DEFAULT '[]',
    selected_telegram_destination_ids JSONB NOT NULL DEFAULT '[]',
    status campaign_status DEFAULT 'draft' NOT NULL,
    total_targets INT DEFAULT 0 NOT NULL,
    processed_targets INT DEFAULT 0 NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

-- 8. CAMPAIGN DISPATCHES & AUDIT LOGS
CREATE TABLE IF NOT EXISTS campaign_dispatches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID REFERENCES campaigns(id) ON DELETE CASCADE NOT NULL,
    contact_id UUID REFERENCES contacts(id) ON DELETE CASCADE NOT NULL,
    target_platform platform_type NOT NULL,
    status dispatch_status DEFAULT 'queued' NOT NULL,
    error_log TEXT,
    idempotency_hash VARCHAR(64) UNIQUE NOT NULL,
    sent_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_dispatch_lookup ON campaign_dispatches(campaign_id, status);

CREATE TABLE IF NOT EXISTS campaign_deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    contact_id UUID REFERENCES contacts(id) ON DELETE CASCADE,
    target_type VARCHAR(50) NOT NULL DEFAULT 'contact' CHECK (target_type IN ('contact', 'telegram_destination')),
    platform VARCHAR(50) NOT NULL,
    routing_value VARCHAR(255) NOT NULL,
    status delivery_status_enum NOT NULL DEFAULT 'sent',
    error_message TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_campaign_deliveries_campaign_id ON campaign_deliveries(campaign_id);
CREATE INDEX IF NOT EXISTS idx_campaign_deliveries_status ON campaign_deliveries(status);
