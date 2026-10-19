-- =========================================================================
-- OMNIPULSE PRODUCTION DATABASE SCHEMA (NEON POSTGRESQL)
-- =========================================================================

-- 1. EXTENSIONS & ENUMS
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE platform_type AS ENUM ('whatsapp', 'telegram', 'x', 'instagram');
CREATE TYPE campaign_status AS ENUM ('draft', 'scheduled', 'pending', 'processing', 'completed', 'failed');
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
    account_identity VARCHAR(255),
    encrypted_credentials JSONB NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending', 'suspended')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_platform_unique ON tenant_channels(tenant_id, platform_name);
CREATE UNIQUE INDEX IF NOT EXISTS uq_active_whatsapp_account_identity
    ON tenant_channels(account_identity)
    WHERE platform_name = 'whatsapp'
      AND status = 'active'
      AND account_identity IS NOT NULL;


CREATE TABLE IF NOT EXISTS contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100),
    channel VARCHAR(50) NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'instagram', 'x')),
    routing_value VARCHAR(255) NOT NULL,
    source VARCHAR(50) NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'csv_import', 'inbound_webhook', 'whatsapp_sync', 'whatsapp_inbound', 'whatsapp_group_sync')),
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
    selected_contact_ids JSONB NOT NULL DEFAULT '[]',
    status campaign_status DEFAULT 'draft' NOT NULL,
    scheduled_at TIMESTAMP WITH TIME ZONE,
    total_targets INT DEFAULT 0 NOT NULL,
    processed_targets INT DEFAULT 0 NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_campaigns_scheduled ON campaigns(status, scheduled_at) WHERE status = 'scheduled';


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

-- 9. AUDIENCE TAGS & SEGMENTATION
CREATE TABLE IF NOT EXISTS tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name VARCHAR(50) NOT NULL,
    color VARCHAR(20) NOT NULL DEFAULT '#6366f1',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_tags_tenant_id ON tags(tenant_id);

CREATE TABLE IF NOT EXISTS contact_tags (
    contact_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (contact_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_contact_tags_contact_id ON contact_tags(contact_id);
CREATE INDEX IF NOT EXISTS idx_contact_tags_tag_id ON contact_tags(tag_id);

-- 10. MESSAGE TEMPLATES LIBRARY
CREATE TABLE IF NOT EXISTS message_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title VARCHAR(150) NOT NULL,
    category VARCHAR(50) NOT NULL DEFAULT 'general',
    body TEXT NOT NULL,
    media_url TEXT,
    variables JSONB DEFAULT '[]'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_message_templates_tenant_id ON message_templates(tenant_id);
CREATE INDEX IF NOT EXISTS idx_message_templates_category ON message_templates(category);

-- 11. WORKSPACE MEMBERSHIPS (MULTI-TENANT RBAC)
CREATE TABLE IF NOT EXISTS tenant_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(50) NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tenant_user UNIQUE (tenant_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_tenant_members_tenant ON tenant_members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_tenant_members_user ON tenant_members(user_id);

-- 12. TEAM INVITATIONS
CREATE TABLE IF NOT EXISTS team_invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL CHECK (role IN ('admin', 'member')),
    token VARCHAR(255) NOT NULL UNIQUE,
    invited_by VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'revoked', 'expired')),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_team_invitations_token ON team_invitations(token);
CREATE INDEX IF NOT EXISTS idx_team_invitations_tenant ON team_invitations(tenant_id);
CREATE INDEX IF NOT EXISTS idx_team_invitations_email ON team_invitations(tenant_id, email);

-- 13. SUBSCRIPTIONS & MONETIZATION (STRIPE & PAYSTACK)
CREATE TABLE IF NOT EXISTS subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    provider VARCHAR(20) NOT NULL DEFAULT 'free' CHECK (provider IN ('free', 'stripe', 'paystack')),
    customer_id VARCHAR(100),
    subscription_id VARCHAR(100),
    plan_tier VARCHAR(50) NOT NULL DEFAULT 'starter' CHECK (plan_tier IN ('starter', 'growth', 'agency')),
    billing_cycle VARCHAR(20) NOT NULL DEFAULT 'monthly' CHECK (billing_cycle IN ('monthly', 'yearly')),
    currency VARCHAR(10) NOT NULL DEFAULT 'USD' CHECK (currency IN ('USD', 'NGN')),
    amount INT NOT NULL DEFAULT 0,
    status VARCHAR(50) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'past_due', 'canceled', 'trialing')),
    current_period_start TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    current_period_end TIMESTAMP WITH TIME ZONE,
    monthly_message_limit INT NOT NULL DEFAULT 1000,
    contacts_limit INT NOT NULL DEFAULT 500,
    channels_limit INT NOT NULL DEFAULT 2,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_subscriptions_tenant UNIQUE (tenant_id)
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_tenant_id ON subscriptions(tenant_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_status ON subscriptions(status);
CREATE INDEX IF NOT EXISTS idx_subscriptions_customer_id ON subscriptions(customer_id);

-- 14. AI BUSINESS ASSISTANT (AUTONOMOUS CLIENT ENGAGEMENT)
CREATE TABLE IF NOT EXISTS ai_agent_configs (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    is_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    operational_mode VARCHAR(32) NOT NULL DEFAULT 'after_hours',
    idle_timeout_seconds INT NOT NULL DEFAULT 180,
    business_name VARCHAR(255) NOT NULL DEFAULT '',
    business_summary TEXT NOT NULL DEFAULT '',
    tone VARCHAR(64) NOT NULL DEFAULT 'professional',
    language VARCHAR(32) NOT NULL DEFAULT 'en',
    custom_system_prompt TEXT NOT NULL DEFAULT '',
    business_hours_start TIME DEFAULT '09:00:00',
    business_hours_end TIME DEFAULT '17:00:00',
    timezone VARCHAR(64) NOT NULL DEFAULT 'Africa/Lagos',
    work_days INT[] DEFAULT '{1,2,3,4,5}',
    human_handoff_message TEXT NOT NULL DEFAULT 'I am transferring you to one of our human team members who will assist you shortly.',
    unsupported_query_message TEXT NOT NULL DEFAULT 'I do not have that exact information right now, but I have alerted our team to follow up with you directly.',
    confidence_threshold NUMERIC(3,2) NOT NULL DEFAULT 0.70,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ai_knowledge_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    category VARCHAR(64) NOT NULL DEFAULT 'faq',
    question_or_topic VARCHAR(500) NOT NULL,
    answer_or_content TEXT NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ai_knowledge_tenant ON ai_knowledge_items(tenant_id, is_active);

CREATE TABLE IF NOT EXISTS ai_knowledge_ingestion_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    filename TEXT NOT NULL,
    content_type VARCHAR(255) NOT NULL DEFAULT 'application/octet-stream',
    source_data BYTEA,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    extracted_items JSONB,
    error_message TEXT,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT chk_ai_knowledge_ingestion_status
        CHECK (status IN ('queued', 'processing', 'ready', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_ai_knowledge_ingestion_tenant_created
    ON ai_knowledge_ingestion_jobs (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ai_knowledge_ingestion_queue
    ON ai_knowledge_ingestion_jobs (status, created_at)
    WHERE status = 'queued';

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS ai_state VARCHAR(32) NOT NULL DEFAULT 'active';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS ai_paused_until TIMESTAMP WITH TIME ZONE;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS needs_human_attention BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_tenant_platform_id
    ON messages (tenant_id, platform_message_id)
    WHERE platform_message_id IS NOT NULL;


