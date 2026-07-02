-- infra/postgres/seeds/000001_dev_contacts.sql

-- Clear out any existing data to ensure idempotent local runs
TRUNCATE TABLE campaign_dispatches, campaigns, contacts CASCADE;

-- Insert Mock Contacts representing various platform states
INSERT INTO contacts (id, first_name, last_name, whatsapp_phone, telegram_chat_id, x_username, is_opted_in)
VALUES 
    -- Scenario A: Complete Omnichannel profile
    ('a1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d', 'Alice', 'Smith', '+12025550143', 5544332211, 'alicesmith_dev', true),
    
    -- Scenario B: Pure WhatsApp User (e.g., entered via customer service phone flow)
    ('b2c3d4e5-f67a-8b9c-0d1e-2f3a4b5c6d7e', 'Bob', 'Jones', '+14155552671', NULL, NULL, true),
    
    -- Scenario C: Pure Telegram User (e.g., interacted with the Telegram Bot directly)
    ('c3d4e5f6-7a8b-9c0d-1e2f-3a4b5c6d7e8f', 'Charlie', 'Brown', NULL, 9988776655, NULL, true),
    
    -- Scenario D: Opted-out User (System must protect and block this dispatch)
    ('d4e5f67a-8b9c-0d1e-2f3a-4b5c6d7e8f9a', 'Malicious', 'Spammer', '+15105559999', 1122334455, 'spambot9000', false);

-- Insert a Sample Mock Campaign
INSERT INTO campaigns (id, title, message_body, external_template_code, status, total_targets)
VALUES 
    ('00000000-0000-0000-0000-000000000001', 'Q3 Product Launch Announcement', 'Hey {{1}}, your early access code for OmniPulse is ready: {{2}}! Check it out.', 'template_v1_launch', 'draft', 3);