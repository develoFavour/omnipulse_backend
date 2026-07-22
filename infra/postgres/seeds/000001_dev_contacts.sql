-- infra/postgres/seeds/000001_dev_contacts.sql

-- Clear out any existing data to ensure idempotent local runs
TRUNCATE TABLE campaign_deliveries, campaigns, contacts, tenant_channels, users, tenants CASCADE;

-- Insert Mock Tenant
INSERT INTO tenants (id, company_name, onboarding_completed)
VALUES ('a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'Acme Corp', true);

-- Insert Mock User
INSERT INTO users (id, tenant_id, email, role)
VALUES ('user-senior-admin-alice', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'alice@acme.com', 'admin');

-- Insert Mock Contacts
INSERT INTO contacts (id, tenant_id, first_name, last_name, channel, routing_value, source, status)
VALUES 
    ('a1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'Alice', 'Smith', 'whatsapp', '+12025550143', 'manual', 'active'),
    ('b2c3d4e5-f67a-8b9c-0d1e-2f3a4b5c6d7e', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'Bob', 'Jones', 'whatsapp', '+14155552671', 'inbound_webhook', 'active'),
    ('c3d4e5f6-7a8b-9c0d-1e2f-3a4b5c6d7e8f', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'Charlie', 'Brown', 'telegram', '9988776655', 'manual', 'active');

-- Insert a Sample Mock Campaign
INSERT INTO campaigns (id, tenant_id, title, message_body, external_template_code, status, delivery_type, selected_channels, total_targets)
VALUES 
    ('00000000-0000-0000-0000-000000000001', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'Q3 Product Launch Announcement', 'Hey, your early access code for OmniPulse is ready!', 'template_v1_launch', 'draft', 'direct_message', '["whatsapp","telegram"]', 3);

-- Insert Mock Channel
INSERT INTO tenant_channels (id, tenant_id, platform_name, sender_identity, encrypted_credentials, status)
VALUES
	('c0eebc99-9c0b-4ef8-bb6d-6bb9bd380a22', 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11', 'whatsapp', '+1234567890', '{"token":"mock_wa"}', 'active');