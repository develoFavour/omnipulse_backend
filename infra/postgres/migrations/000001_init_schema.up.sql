CREATE TYPE platform_type AS ENUM ('whatsapp', 'telegram', 'x');
CREATE TYPE campaign_status AS ENUM ('draft', 'pending', 'processing', 'completed', 'failed');
CREATE TYPE dispatch_status AS ENUM ('queued', 'in_flight', 'rate_limited', 'delivered', 'failed');

CREATE TABLE contacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    first_name VARCHAR(100) NOT NULL,
    last_name VARCHAR(100),
    whatsapp_phone VARCHAR(20) UNIQUE,
    telegram_chat_id BIGINT UNIQUE,
    x_username VARCHAR(50) UNIQUE,
    is_opted_in BOOLEAN DEFAULT TRUE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE INDEX idx_contacts_whatsapp ON contacts(whatsapp_phone) WHERE whatsapp_phone IS NOT NULL;
CREATE INDEX idx_contacts_telegram ON contacts(telegram_chat_id) WHERE telegram_chat_id IS NOT NULL;

CREATE TABLE campaigns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title VARCHAR(255) NOT NULL,
    message_body TEXT NOT NULL,
    external_template_code VARCHAR(100), 
    media_url TEXT,
    status campaign_status DEFAULT 'draft' NOT NULL,
    total_targets INT DEFAULT 0 NOT NULL,
    processed_targets INT DEFAULT 0 NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

CREATE TABLE campaign_dispatches (
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

CREATE INDEX idx_dispatch_lookup ON campaign_dispatches(campaign_id, status);