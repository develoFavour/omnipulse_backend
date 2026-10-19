package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"omnipulse/apps/api-gateway/internal/config"
	"omnipulse/apps/api-gateway/internal/event"
	"omnipulse/apps/api-gateway/internal/handler"

	"omnipulse/apps/api-gateway/internal/repository"
	"omnipulse/apps/api-gateway/internal/service"
	"omnipulse/apps/api-gateway/internal/usecase"
	"omnipulse/apps/api-gateway/internal/utils"
	"omnipulse/apps/api-gateway/internal/worker"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/nats-io/nats.go"
	"github.com/rs/cors"
)

func main() {
	logger := log.New(os.Stdout, "[API-GATEWAY] ", log.LstdFlags|log.Lshortfile)
	loadEnv(logger)

	cfg := config.Load()

	clerk.SetKey(cfg.ClerkSecretKey)

	// 1. Establish Database Connection Pool
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		logger.Fatalf("Database connection initialization failed: %v", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		logger.Fatalf("Database cluster unreachable: %v", err)
	}
	logger.Printf("Attached to PostgreSQL database pool [Mode: %s].\n", cfg.Environment)

	// WhatsApp account ownership is global: an active WhatsApp account may
	// belong to only one workspace. This is idempotent for existing databases
	// and intentionally fails startup if legacy duplicate mappings remain, so
	// the application can never run without the protection in place.
	if _, err := db.Exec(`
		ALTER TABLE tenant_channels
			ADD COLUMN IF NOT EXISTS account_identity VARCHAR(255);
		UPDATE tenant_channels
		SET account_identity = 'phone:' || NULLIF(regexp_replace(sender_identity, '[^0-9]', '', 'g'), '')
		WHERE platform_name = 'whatsapp' AND account_identity IS NULL;
		CREATE UNIQUE INDEX IF NOT EXISTS uq_active_whatsapp_account_identity
			ON tenant_channels(account_identity)
			WHERE platform_name = 'whatsapp'
			  AND status = 'active'
			  AND account_identity IS NOT NULL;
	`); err != nil {
		logger.Fatalf("[DB-MIGRATE] WhatsApp workspace ownership protection failed: %v", err)
	}
	logger.Println("[DB-MIGRATE] WhatsApp account ownership protection is active.")

	// Additive channel health metadata. These fields make connection state
	// visible to the workspace without changing delivery behavior.
	if _, err := db.Exec(`
		ALTER TABLE tenant_channels ADD COLUMN IF NOT EXISTS health_status VARCHAR(50) NOT NULL DEFAULT 'unknown';
		ALTER TABLE tenant_channels ADD COLUMN IF NOT EXISTS last_heartbeat_at TIMESTAMP WITH TIME ZONE;
		ALTER TABLE tenant_channels ADD COLUMN IF NOT EXISTS last_successful_at TIMESTAMP WITH TIME ZONE;
		ALTER TABLE tenant_channels ADD COLUMN IF NOT EXISTS last_error TEXT;
		ALTER TABLE tenant_channels ADD COLUMN IF NOT EXISTS last_error_at TIMESTAMP WITH TIME ZONE;
	`); err != nil {
		logger.Fatalf("[DB-MIGRATE] Channel health metadata migration failed: %v", err)
	}
	logger.Println("[DB-MIGRATE] Channel health metadata is available.")

	// Idempotent schema migrations: ensure users.id supports Clerk string IDs and campaigns have selected_contact_ids
	_, _ = db.Exec(`ALTER TABLE users ALTER COLUMN id DROP DEFAULT;`)
	if _, err := db.Exec(`ALTER TABLE users ALTER COLUMN id TYPE VARCHAR(255) USING id::varchar(255);`); err != nil {
		logger.Printf("[DB-MIGRATE] Alter users.id column type: %v\n", err)
	} else {
		logger.Println("[DB-MIGRATE] Successfully ensured users.id is VARCHAR(255).")
	}
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS first_name VARCHAR(100) NOT NULL DEFAULT ''; ALTER TABLE users ADD COLUMN IF NOT EXISTS last_name VARCHAR(100) NOT NULL DEFAULT ''; CREATE TABLE IF NOT EXISTS tenant_settings (tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE, notification_preferences JSONB NOT NULL DEFAULT '[]', logo_url TEXT NOT NULL DEFAULT '', timezone VARCHAR(100) NOT NULL DEFAULT 'UTC', language VARCHAR(20) NOT NULL DEFAULT 'en-US', updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()); ALTER TABLE tenant_settings ADD COLUMN IF NOT EXISTS logo_url TEXT NOT NULL DEFAULT ''; CREATE TABLE IF NOT EXISTS api_keys (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE, name VARCHAR(100) NOT NULL, prefix VARCHAR(32) NOT NULL, key_hash VARCHAR(128) NOT NULL UNIQUE, created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(), last_used_at TIMESTAMP WITH TIME ZONE, revoked_at TIMESTAMP WITH TIME ZONE); CREATE INDEX IF NOT EXISTS idx_api_keys_tenant_active ON api_keys(tenant_id) WHERE revoked_at IS NULL;`)
	if _, err := db.Exec(`ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS selected_contact_ids JSONB NOT NULL DEFAULT '[]';`); err != nil {
		logger.Printf("[DB-MIGRATE] Add selected_contact_ids column: %v\n", err)
	} else {
		logger.Println("[DB-MIGRATE] Successfully ensured campaigns.selected_contact_ids column exists.")
	}

	// Idempotent schema migrations: campaign approval workflow & agency settings
	if _, err := db.Exec(`
		ALTER TABLE tenant_settings ADD COLUMN IF NOT EXISTS require_campaign_approval BOOLEAN NOT NULL DEFAULT false;
		ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS submitted_by VARCHAR(255);
		ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS approved_by VARCHAR(255);
		ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS approved_at TIMESTAMP WITH TIME ZONE;
		ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS rejection_reason TEXT;
		DO $$ BEGIN
			ALTER TYPE campaign_status ADD VALUE IF NOT EXISTS 'pending_approval';
			ALTER TYPE campaign_status ADD VALUE IF NOT EXISTS 'approved';
			ALTER TYPE campaign_status ADD VALUE IF NOT EXISTS 'rejected';
		EXCEPTION
			WHEN duplicate_object THEN null;
			WHEN undefined_object THEN null;
			WHEN others THEN null;
		END $$;
		ALTER TABLE campaigns ALTER COLUMN status TYPE VARCHAR(50) USING status::varchar(50);
	`); err != nil {
		logger.Printf("[DB-MIGRATE] Campaign approval columns migration: %v\n", err)
	} else {
		logger.Println("[DB-MIGRATE] Successfully ensured campaign approval schema columns exist.")
	}

	// Whatsmeow requires these keys for its ON CONFLICT upserts. Keep this
	// idempotent so migrated or freshly provisioned databases behave alike.
	if _, err := db.Exec(`
		DO $$ BEGIN
			IF to_regclass('public.whatsmeow_device') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_device_pkey') THEN
				ALTER TABLE whatsmeow_device ADD CONSTRAINT whatsmeow_device_pkey PRIMARY KEY (jid);
			END IF;
			IF to_regclass('public.whatsmeow_identity_keys') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_identity_keys_pkey') THEN
				ALTER TABLE whatsmeow_identity_keys ADD CONSTRAINT whatsmeow_identity_keys_pkey PRIMARY KEY (our_jid, their_id);
			END IF;
			IF to_regclass('public.whatsmeow_pre_keys') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_pre_keys_pkey') THEN
				ALTER TABLE whatsmeow_pre_keys ADD CONSTRAINT whatsmeow_pre_keys_pkey PRIMARY KEY (jid, key_id);
			END IF;
			IF to_regclass('public.whatsmeow_sessions') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_sessions_pkey') THEN
				ALTER TABLE whatsmeow_sessions ADD CONSTRAINT whatsmeow_sessions_pkey PRIMARY KEY (our_jid, their_id);
			END IF;
			IF to_regclass('public.whatsmeow_sender_keys') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_sender_keys_pkey') THEN
				ALTER TABLE whatsmeow_sender_keys ADD CONSTRAINT whatsmeow_sender_keys_pkey PRIMARY KEY (our_jid, chat_id, sender_id);
			END IF;
			IF to_regclass('public.whatsmeow_app_state_sync_keys') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_app_state_sync_keys_pkey') THEN
				ALTER TABLE whatsmeow_app_state_sync_keys ADD CONSTRAINT whatsmeow_app_state_sync_keys_pkey PRIMARY KEY (jid, key_id);
			END IF;
			IF to_regclass('public.whatsmeow_app_state_version') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_app_state_version_pkey') THEN
				ALTER TABLE whatsmeow_app_state_version ADD CONSTRAINT whatsmeow_app_state_version_pkey PRIMARY KEY (jid, name);
			END IF;
			IF to_regclass('public.whatsmeow_app_state_mutation_macs') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_app_state_mutation_macs_pkey') THEN
				ALTER TABLE whatsmeow_app_state_mutation_macs ADD CONSTRAINT whatsmeow_app_state_mutation_macs_pkey PRIMARY KEY (jid, name, version, index_mac);
			END IF;
			IF to_regclass('public.whatsmeow_contacts') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_contacts_pkey') THEN
				ALTER TABLE whatsmeow_contacts ADD CONSTRAINT whatsmeow_contacts_pkey PRIMARY KEY (our_jid, their_jid);
			END IF;
			IF to_regclass('public.whatsmeow_chat_settings') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_chat_settings_pkey') THEN
				ALTER TABLE whatsmeow_chat_settings ADD CONSTRAINT whatsmeow_chat_settings_pkey PRIMARY KEY (our_jid, chat_jid);
			END IF;
			IF to_regclass('public.whatsmeow_message_secrets') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_message_secrets_pkey') THEN
				ALTER TABLE whatsmeow_message_secrets ADD CONSTRAINT whatsmeow_message_secrets_pkey PRIMARY KEY (our_jid, chat_jid, sender_jid, message_id);
			END IF;
			IF to_regclass('public.whatsmeow_privacy_tokens') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_privacy_tokens_pkey') THEN
				ALTER TABLE whatsmeow_privacy_tokens ADD CONSTRAINT whatsmeow_privacy_tokens_pkey PRIMARY KEY (our_jid, their_jid);
			END IF;
			IF to_regclass('public.whatsmeow_lid_map') IS NOT NULL THEN
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_lid_map_pkey') THEN
					ALTER TABLE whatsmeow_lid_map ADD CONSTRAINT whatsmeow_lid_map_pkey PRIMARY KEY (lid);
				END IF;
				IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_lid_map_pn_key') THEN
					ALTER TABLE whatsmeow_lid_map ADD CONSTRAINT whatsmeow_lid_map_pn_key UNIQUE (pn);
				END IF;
			END IF;
			IF to_regclass('public.whatsmeow_nct_salt') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_nct_salt_pkey') THEN
				ALTER TABLE whatsmeow_nct_salt ADD CONSTRAINT whatsmeow_nct_salt_pkey PRIMARY KEY (our_jid);
			END IF;
			IF to_regclass('public.whatsmeow_event_buffer') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_event_buffer_pkey') THEN
				ALTER TABLE whatsmeow_event_buffer ADD CONSTRAINT whatsmeow_event_buffer_pkey PRIMARY KEY (our_jid, ciphertext_hash);
			END IF;
			IF to_regclass('public.whatsmeow_retry_buffer') IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsmeow_retry_buffer_pkey') THEN
				ALTER TABLE whatsmeow_retry_buffer ADD CONSTRAINT whatsmeow_retry_buffer_pkey PRIMARY KEY (our_jid, chat_jid, message_id);
			END IF;
		EXCEPTION WHEN duplicate_object THEN NULL;
		END $$;
	`); err != nil {
		logger.Printf("[DB-MIGRATE] Whatsmeow constraints migration: %v\n", err)
	} else {
		logger.Println("[DB-MIGRATE] Successfully ensured Whatsmeow constraints exist.")
	}

	// Idempotent indexes for contacts table search and pagination performance
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_contacts_tenant_created ON contacts(tenant_id, created_at DESC);`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_contacts_tenant_channel ON contacts(tenant_id, channel);`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_contacts_tenant_name ON contacts(tenant_id, first_name);`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_contacts_tenant_routing ON contacts(tenant_id, routing_value);`)

	// Idempotent schema migrations: notifications table
	_, _ = db.Exec(`DO $$ BEGIN
		CREATE TYPE notification_type AS ENUM (
			'campaign_completed',
			'delivery_failure',
			'new_opt_out',
			'contact_import_finished',
			'channel_disconnected'
		);
	EXCEPTION
		WHEN duplicate_object THEN null;
	END $$;`)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS notifications (
		id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
		type        notification_type NOT NULL,
		title       TEXT NOT NULL,
		body        TEXT NOT NULL,
		metadata    JSONB NOT NULL DEFAULT '{}',
		is_read     BOOLEAN NOT NULL DEFAULT FALSE,
		created_at  TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
	);`); err != nil {
		logger.Printf("[DB-MIGRATE] notifications table create: %v\n", err)
	} else {
		_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_notifications_tenant_read ON notifications(tenant_id, is_read, created_at DESC);`)
		logger.Println("[DB-MIGRATE] Successfully ensured notifications table and indexes exist.")
	}

	// Idempotent schema migrations: Unified Team Inbox (conversations & messages)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS conversations (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
			contact_id UUID NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
			channel VARCHAR(50) NOT NULL CHECK (channel IN ('whatsapp', 'telegram', 'instagram', 'x')),
			status VARCHAR(50) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed', 'waiting')),
			assigned_to VARCHAR(255) REFERENCES users(id) ON DELETE SET NULL,
			last_message_text TEXT,
			last_message_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			unread_count INT NOT NULL DEFAULT 0,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
			CONSTRAINT uq_tenant_contact_channel UNIQUE (tenant_id, contact_id, channel)
		);
		CREATE INDEX IF NOT EXISTS idx_conversations_tenant_status ON conversations(tenant_id, status, last_message_at DESC);
		CREATE INDEX IF NOT EXISTS idx_conversations_contact ON conversations(contact_id);
		CREATE INDEX IF NOT EXISTS idx_conversations_assigned ON conversations(assigned_to);

		CREATE TABLE IF NOT EXISTS messages (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
			conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
			direction VARCHAR(20) NOT NULL CHECK (direction IN ('inbound', 'outbound')),
			sender_type VARCHAR(20) NOT NULL DEFAULT 'contact' CHECK (sender_type IN ('contact', 'agent', 'bot', 'system')),
			sender_id VARCHAR(255),
			body TEXT NOT NULL,
			media_url TEXT,
			media_type VARCHAR(50),
			platform_message_id VARCHAR(255),
			status VARCHAR(50) NOT NULL DEFAULT 'sent' CHECK (status IN ('sent', 'delivered', 'read', 'failed')),
			error_message TEXT,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_messages_conversation ON messages(conversation_id, created_at ASC);
		CREATE INDEX IF NOT EXISTS idx_messages_tenant ON messages(tenant_id, created_at DESC);
	`); err != nil {
		logger.Printf("[DB-MIGRATE] conversations & messages tables create: %v\n", err)
	} else {
		logger.Println("[DB-MIGRATE] Successfully ensured conversations and messages tables exist.")
	}

	// Idempotent: add is_deleted and edited_at columns for edit/delete message support
	for _, col := range []string{
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited_at TIMESTAMP WITH TIME ZONE`,
	} {
		if _, err := db.Exec(col); err != nil {
			logger.Printf("[DB-MIGRATE] messages column migration: %v\n", err)
		}
	}

	// Idempotent index for fast aggregate time-series queries
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_campaign_deliveries_created ON campaign_deliveries(created_at DESC);`)

	// Idempotent schema migrations: team management & invitations
	if _, err := db.Exec(`
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

		INSERT INTO tenant_members (tenant_id, user_id, role)
		SELECT tenant_id, id, CASE WHEN role = 'admin' THEN 'owner' ELSE role END
		FROM users
		ON CONFLICT (tenant_id, user_id) DO NOTHING;
	`); err != nil {
		logger.Printf("[DB-MIGRATE] team management tables create: %v\n", err)
	} else {
		logger.Println("[DB-MIGRATE] Successfully ensured tenant_members and team_invitations tables exist.")
	}

	// Idempotent schema migrations: subscriptions & billing
	if _, err := db.Exec(`
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
	`); err != nil {
		logger.Printf("[DB-MIGRATE] subscriptions table create: %v\n", err)
	} else {
		logger.Println("[DB-MIGRATE] Successfully ensured subscriptions table and indexes exist.")
	}

	// Idempotent schema migrations: AI Business Assistant
	if _, err := db.Exec(`
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
		ALTER TABLE ai_knowledge_ingestion_jobs
			ALTER COLUMN source_data DROP NOT NULL;

		ALTER TABLE conversations ADD COLUMN IF NOT EXISTS ai_state VARCHAR(32) NOT NULL DEFAULT 'active';
		ALTER TABLE conversations ADD COLUMN IF NOT EXISTS ai_paused_until TIMESTAMP WITH TIME ZONE;
		ALTER TABLE conversations ADD COLUMN IF NOT EXISTS needs_human_attention BOOLEAN NOT NULL DEFAULT FALSE;
		CREATE UNIQUE INDEX IF NOT EXISTS uq_messages_tenant_platform_id
			ON messages (tenant_id, platform_message_id)
			WHERE platform_message_id IS NOT NULL;
	`); err != nil {
		logger.Printf("[DB-MIGRATE] AI Business Assistant table migration: %v\n", err)
	} else {
		logger.Println("[DB-MIGRATE] Successfully ensured AI Business Assistant tables and conversation columns exist.")
	}

	// 2. Initialize NATS JetStream Event Broker Adapter
	natsPublisher, err := event.NewJetStreamPublisher(cfg.NatsURL, cfg.NatsCreds)
	if err != nil {
		logger.Fatalf("Failed to initialize NATS streaming fabric core: %v", err)
	}
	logger.Println("Successfully connected to NATS JetStream fabric.")

	var aiDispatcher service.AIReplyDispatcher
	var knowledgeDispatcher service.KnowledgeIngestionDispatcher
	var aiReplyJS nats.JetStreamContext
	if jp, ok := natsPublisher.(*event.JetStreamPublisher); ok {
		_, aiReplyJS = jp.GetConn()
		aiDispatcher = service.NewNatsAIReplyDispatcher(aiReplyJS)
		knowledgeDispatcher = service.NewNatsKnowledgeIngestionDispatcher(aiReplyJS)
	}

	subRepo := repository.NewPostgresSubscriptionRepository(db)
	contactRepo := repository.NewPostgresContactRepository(db)
	campaignRepo := repository.NewPostgresCampaignRepository(db)
	identityRepo := repository.NewPostgresIdentityRepository(db)
	channelRepo := repository.NewPostgresChannelRepository(db, cfg.EncryptionKey)
	dashboardRepo := repository.NewPostgresDashboardRepository(db)
	destinationRepo := repository.NewPostgresTelegramDestinationRepository(db)
	tagRepo := repository.NewPostgresTagRepository(db)
	templateRepo := repository.NewPostgresTemplateRepository(db)
	aiAgentRepo := repository.NewPostgresAIAgentRepository(db)
	notificationRepo := repository.NewPostgresNotificationRepository(db)
	analyticsRepo := repository.NewPostgresAnalyticsRepository(db)
	inboxRepo := repository.NewPostgresInboxRepository(db)

	tagUseCase := usecase.NewTagUseCase(tagRepo)
	templateUseCase := usecase.NewTemplateUseCase(templateRepo)
	contactUseCase := usecase.NewContactUseCase(contactRepo, tagRepo, subRepo)
	campaignUseCase := usecase.NewCampaignUseCase(campaignRepo, contactRepo, destinationRepo, natsPublisher, subRepo)
	identityUseCase := usecase.NewIdentityUseCase(identityRepo, channelRepo, subRepo)
	dashboardUseCase := usecase.NewDashboardUseCase(dashboardRepo)
	notificationUseCase := usecase.NewNotificationUseCase(notificationRepo)
	analyticsUseCase := usecase.NewAnalyticsUseCase(analyticsRepo)

	tagHandler := handler.NewTagHandler(tagUseCase)
	templateHandler := handler.NewTemplateHandler(templateUseCase)
	contactHandler := handler.NewContactHandler(contactUseCase, subRepo)
	campaignHandler := handler.NewCampaignHandler(campaignUseCase, subRepo)
	identityHandler := handler.NewIdentityHandler(identityUseCase)
	settingsHandler := handler.NewSettingsHandler(db, subRepo)
	notificationHandler := handler.NewNotificationHandler(notificationUseCase)
	analyticsHandler := handler.NewAnalyticsHandler(analyticsUseCase)

	mailer := utils.NewMailer(cfg)
	teamUseCase := usecase.NewTeamUseCase(identityRepo, mailer, cfg.PublicAppBaseURL, logger, subRepo)
	teamHandler := handler.NewTeamHandler(teamUseCase)

	stripeSvc := service.NewStripeService(cfg.StripeSecretKey, cfg.StripeWebhookSecret)
	paystackSvc := service.NewPaystackService(cfg.PaystackSecretKey, cfg.PaystackPublicKey)
	billingUseCase := usecase.NewBillingUseCase(subRepo, stripeSvc, paystackSvc, cfg.PublicAppBaseURL)
	billingHandler := handler.NewBillingHandler(billingUseCase)
	aiAgentHandler := handler.NewAIAgentHandler(aiAgentRepo)

	aiEngine := service.NewAIEngineService(cfg.GeminiAPIKey, cfg.GroqAPIKey)
	aiAgentHandler.SetAIEngine(aiEngine)
	aiAgentHandler.SetKnowledgeIngestionDispatcher(knowledgeDispatcher)
	if aiEngine.HasActiveProvider() {
		logger.Printf("[AI-ENGINE] 🤖 Dual-engine AI Business Assistant initialized (Gemini: %v, Groq: %v)\n", cfg.GeminiAPIKey != "", cfg.GroqAPIKey != "")
	} else {
		logger.Println("[AI-ENGINE] ℹ️ Dual-engine AI Business Assistant standing by (set GEMINI_API_KEY or GROQ_API_KEY to activate)")
	}
	if aiReplyJS != nil {
		knowledgeWorker := service.NewKnowledgeIngestionWorker(aiReplyJS, aiAgentRepo, aiEngine)
		if err := knowledgeWorker.Start(); err != nil {
			logger.Printf("[KNOWLEDGE-QUEUE] Failed to start durable ingestion worker: %v\n", err)
		} else {
			logger.Println("[KNOWLEDGE-QUEUE] Durable document ingestion worker started")
		}
	}

	var waManager *service.WhatsAppManager
	var waErr error
	waManager, waErr = service.NewWhatsAppManager(db, cfg.EncryptionKey)
	if waErr != nil {
		logger.Printf("[WA-WARN] WhatsApp Multi-Device manager initialization deferred: %v\n", waErr)
	} else {
		logger.Println("Successfully initialized WhatsApp Multi-Device session manager.")
		waManager.SetInboxRepo(inboxRepo)
		waManager.SetAIEngine(aiEngine)
		waManager.SetAIAgentRepo(aiAgentRepo)
		waManager.SetAIReplyDispatcher(aiDispatcher)
	}

	channelHandler := handler.NewChannelHandler(channelRepo, contactRepo, subRepo, cfg.PublicAPIBaseURL, cfg.PublicAppBaseURL, handler.MetaAppConfig{
		AppID:         cfg.MetaAppID,
		AppSecret:     cfg.MetaAppSecret,
		WABAConfigID:  cfg.MetaWABAConfigID,
		WABAID:        cfg.MetaWABAID,
		PhoneNumberID: cfg.MetaPhoneNumberID,
	}, waManager)
	webhookHandler := handler.NewWebhookHandler(contactUseCase, channelRepo, destinationRepo, inboxRepo, cfg.MetaAppSecret, cfg.TelegramWebhookSecret)
	webhookHandler.SetAIEngine(aiEngine)
	webhookHandler.SetAIAgentRepo(aiAgentRepo)
	webhookHandler.SetAIReplyDispatcher(aiDispatcher)
	if aiReplyJS != nil {
		aiReplyWorker := service.NewAIReplyWorker(aiReplyJS, func(ctx context.Context, job service.AIReplyJob) error {
			if job.Channel == "whatsapp" && waManager != nil {
				return waManager.ProcessAIReplyJob(ctx, job)
			}
			if job.Channel == "telegram" {
				return webhookHandler.ProcessAIReplyJob(ctx, job)
			}
			return fmt.Errorf("unsupported AI reply channel: %s", job.Channel)
		})
		if err := aiReplyWorker.Start(); err != nil {
			logger.Printf("[AI-QUEUE] Failed to start durable AI reply worker: %v\n", err)
		} else {
			logger.Println("[AI-QUEUE] Durable AI reply worker started")
		}
	}

	dashboardHandler := handler.NewDashboardHandler(dashboardUseCase, subRepo)
	destinationHandler := handler.NewTelegramDestinationHandler(destinationRepo)
	inboxUseCase := usecase.NewInboxUseCase(inboxRepo, contactRepo, channelRepo, waManager)
	inboxUseCase.SetAIAgentRepo(aiAgentRepo)
	inboxAssignmentNotifier := handler.NewAssignmentEmailNotifier(db, mailer, cfg.PublicAppBaseURL, logger)
	inboxHandler := handler.NewInboxHandler(inboxUseCase, inboxAssignmentNotifier)

	inboxHub := service.NewInboxHub()
	aiAgentHandler.SetInboxHub(inboxHub)
	if waManager != nil {
		waManager.SetInboxHub(inboxHub)
	}
	webhookHandler.SetBroadcaster(inboxHub)
	inboxUseCase.SetBroadcaster(inboxHub)

	var mediaService *service.MediaService
	if cfg.CloudinaryURL != "" {
		ms, err := service.NewMediaService(cfg.CloudinaryURL)
		if err != nil {
			logger.Printf("[MEDIA-WARN] Cloudinary media service initialization failed: %v\n", err)
		} else {
			mediaService = ms
			logger.Println("[MEDIA] Cloudinary media service initialized successfully")
		}
	} else {
		logger.Println("[MEDIA-WARN] CLOUDINARY_URL is not set; media uploads will be unavailable")
	}
	mediaHandler := handler.NewMediaHandler(mediaService)

	globalWorkerCtx, cancelWorkers := context.WithCancel(context.Background())
	var natsConn *nats.Conn
	var natsJS nats.JetStreamContext
	if jp, ok := natsPublisher.(*event.JetStreamPublisher); ok {
		natsConn, natsJS = jp.GetConn()
	}

	campaignHub := service.NewCampaignHub(campaignRepo)

	telemetryWorker, err := worker.NewTelemetryConsumer(cfg.NatsURL, cfg.NatsCreds, natsConn, natsJS, campaignRepo, notificationRepo)
	if err != nil {
		logger.Printf("[NATS-WARN] Telemetry worker initialization deferred: %v\n", err)
	} else {
		telemetryWorker.SetHub(campaignHub)
		if err := telemetryWorker.Start(globalWorkerCtx); err != nil {
			logger.Printf("[NATS-WARN] Telemetry stream subscription deferred: %v\n", err)
		} else {
			defer telemetryWorker.Stop()
		}
	}

	var broadcastWorker *worker.BroadcastConsumer
	bw, err := worker.NewBroadcastConsumer(natsConn, natsJS, db, waManager, cfg.EncryptionKey)
	if err != nil {
		logger.Printf("[NATS-WARN] Broadcast delivery worker initialization deferred: %v\n", err)
	} else {
		broadcastWorker = bw
		if err := broadcastWorker.Start(globalWorkerCtx); err != nil {
			logger.Printf("[NATS-WARN] Broadcast stream subscription deferred: %v\n", err)
			broadcastWorker = nil // mark as nil so health endpoint reflects reality
		} else {
			defer broadcastWorker.Stop()
		}
	}

	// Campaign Watchdog — detects stuck campaigns and fires in-app alerts
	campaignWatchdog := worker.NewCampaignWatchdog(db, notificationRepo)
	campaignWatchdog.Start(globalWorkerCtx)

	// Campaign Scheduler — polls every 15s for due scheduled campaigns
	schedulerService := service.NewSchedulerService(campaignRepo, campaignUseCase)
	schedulerService.Start(globalWorkerCtx)

	// 4. Modern Native HTTP Routing Multiplexer
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "healthy", "service": "api-gateway"})
	})

	// /health/workers — live NATS consumer health check
	// Queries JetStream directly to count active consumers on the CAMPAIGNS stream.
	// A consumer count of 0 means no worker is subscribed and broadcasts will queue silently.
	mux.HandleFunc("GET /health/workers", func(w http.ResponseWriter, r *http.Request) {
		type workerStatus struct {
			Service        string `json:"service"`
			Subscribed     bool   `json:"subscribed"`
			NumPending     uint64 `json:"num_pending"`
			NumAckPending  int    `json:"num_ack_pending"`
			ConsumerExists bool   `json:"consumer_exists"`
		}
		type healthResponse struct {
			Status          string       `json:"status"`
			BroadcastWorker workerStatus `json:"broadcast_worker"`
			TelemetryWorker workerStatus `json:"telemetry_worker"`
			NATSStreamMsgs  uint64       `json:"nats_stream_msgs"`
			CheckedAt       string       `json:"checked_at"`
		}

		resp := healthResponse{
			Status:    "healthy",
			CheckedAt: time.Now().UTC().Format(time.RFC3339),
		}

		if natsJS != nil {
			if info, err := natsJS.StreamInfo("CAMPAIGNS"); err == nil {
				resp.NATSStreamMsgs = info.State.Msgs
			}
			// Broadcast delivery pool consumer
			if ci, err := natsJS.ConsumerInfo("CAMPAIGNS", "broadcast-delivery-pool"); err == nil {
				resp.BroadcastWorker = workerStatus{
					Service:        "broadcast-worker",
					Subscribed:     ci.NumAckPending > 0 || ci.Delivered.Stream > 0,
					NumPending:     ci.NumPending,
					NumAckPending:  ci.NumAckPending,
					ConsumerExists: true,
				}
			} else {
				resp.BroadcastWorker = workerStatus{Service: "broadcast-worker", ConsumerExists: false}
				resp.Status = "degraded"
			}
			// Telemetry consumer (durable name registered in telemetry_sub.go)
			if ci, err := natsJS.ConsumerInfo("CAMPAIGNS", "telemetry-gateway-group"); err == nil {
				resp.TelemetryWorker = workerStatus{
					Service:        "telemetry-worker",
					Subscribed:     ci.NumAckPending > 0 || ci.Delivered.Stream > 0,
					NumPending:     ci.NumPending,
					NumAckPending:  ci.NumAckPending,
					ConsumerExists: true,
				}
			} else {
				resp.TelemetryWorker = workerStatus{Service: "telemetry-worker", ConsumerExists: false}
			}
		} else {
			resp.Status = "degraded"
		}

		statusCode := http.StatusOK
		if resp.Status == "degraded" {
			statusCode = http.StatusServiceUnavailable
		}
		utils.WriteJSON(w, statusCode, resp)
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "healthy", "service": "api-gateway"})
	})

	// Identity Subsystem Endpoints
	mux.HandleFunc("POST /api/v1/auth/sync", identityHandler.SyncUser)
	mux.HandleFunc("PATCH /api/v1/onboarding/brand", identityHandler.UpdateBrand)
	mux.HandleFunc("POST /api/v1/onboarding/complete", identityHandler.CompleteOnboarding)

	// Multi-Workspace Endpoints
	mux.HandleFunc("GET /api/v1/workspaces", identityHandler.ListWorkspaces)
	mux.HandleFunc("POST /api/v1/workspaces/switch", identityHandler.SwitchWorkspace)
	mux.HandleFunc("POST /api/v1/workspaces", identityHandler.CreateWorkspace)
	mux.Handle("DELETE /api/v1/workspaces", handler.RequireRole("owner")(http.HandlerFunc(identityHandler.DeleteWorkspace)))
	mux.HandleFunc("GET /api/v1/profile", settingsHandler.GetProfile)
	mux.HandleFunc("PATCH /api/v1/profile", settingsHandler.UpdateProfile)
	mux.HandleFunc("GET /api/v1/notification-preferences", settingsHandler.GetPreferences)
	mux.HandleFunc("PUT /api/v1/notification-preferences", settingsHandler.UpdatePreferences)
	mux.HandleFunc("GET /api/v1/workspace-settings", settingsHandler.GetWorkspaceSettings)
	mux.HandleFunc("PATCH /api/v1/workspace-settings", settingsHandler.UpdateWorkspaceSettings)
	mux.HandleFunc("GET /api/v1/settings/approval", settingsHandler.GetApprovalSettings)
	mux.Handle("PUT /api/v1/settings/approval", handler.RequireRole("owner", "admin")(http.HandlerFunc(settingsHandler.UpdateApprovalSettings)))
	mux.HandleFunc("GET /api/v1/api-keys", settingsHandler.ListAPIKeys)
	mux.HandleFunc("POST /api/v1/api-keys", settingsHandler.CreateAPIKey)
	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", settingsHandler.RevokeAPIKey)

	// Team Management & Invitation Endpoints
	mux.HandleFunc("GET /api/v1/team/members", teamHandler.ListTeam)
	mux.Handle("POST /api/v1/team/invite", handler.RequireRole("owner", "admin")(http.HandlerFunc(teamHandler.InviteMember)))
	mux.Handle("DELETE /api/v1/team/invitations/{id}", handler.RequireRole("owner", "admin")(http.HandlerFunc(teamHandler.RevokeInvitation)))
	mux.Handle("DELETE /api/v1/team/members/{id}", handler.RequireRole("owner")(http.HandlerFunc(teamHandler.RemoveMember)))
	mux.Handle("PATCH /api/v1/team/members/{id}/role", handler.RequireRole("owner")(http.HandlerFunc(teamHandler.UpdateMemberRole)))

	// Invitation Acceptance & Preview
	mux.HandleFunc("GET /api/v1/invitations/preview", teamHandler.GetInvitationPreview)
	mux.HandleFunc("POST /api/v1/invitations/accept", teamHandler.AcceptInvitation)

	// Channel Subsystem Endpoints (Restricted: Owner & Admin only)
	mux.Handle("POST /api/v1/channels", handler.RequireRole("owner", "admin")(http.HandlerFunc(channelHandler.CreateChannel)))
	mux.HandleFunc("GET /api/v1/channels", channelHandler.ListChannels)
	mux.Handle("DELETE /api/v1/channels/{platform}", handler.RequireRole("owner", "admin")(http.HandlerFunc(channelHandler.HandleDisconnectChannel)))

	// WhatsApp Multi-Device QR Endpoints
	mux.Handle("GET /api/v1/channels/whatsapp/qr", handler.RequireRole("owner", "admin")(http.HandlerFunc(channelHandler.HandleWhatsAppQR)))
	mux.HandleFunc("GET /api/v1/channels/whatsapp/status", channelHandler.HandleWhatsAppStatus)
	mux.Handle("POST /api/v1/channels/whatsapp/disconnect", handler.RequireRole("owner", "admin")(http.HandlerFunc(channelHandler.HandleWhatsAppDisconnect)))
	mux.Handle("POST /api/v1/channels/whatsapp/sync-contacts", handler.RequireRole("owner", "admin")(http.HandlerFunc(channelHandler.HandleWhatsAppSyncContacts)))

	// WhatsApp Embedded Signup (1-Click OAuth) Endpoints
	mux.HandleFunc("GET /api/v1/channels/whatsapp/oauth/config", channelHandler.HandleWhatsAppOAuthConfig)
	mux.Handle("POST /api/v1/channels/whatsapp/oauth/callback", handler.RequireRole("owner", "admin")(http.HandlerFunc(channelHandler.HandleWhatsAppOAuthCallback)))

	// Telegram Destination Endpoints
	mux.HandleFunc("GET /api/v1/telegram/destinations", destinationHandler.ListDestinations)
	mux.Handle("POST /api/v1/channels/telegram/sync-contacts", handler.RequireRole("owner", "admin")(http.HandlerFunc(channelHandler.HandleTelegramSyncContacts)))

	// Contact Subsystem Endpoints
	mux.Handle("GET /api/v1/contacts/export", handler.RequireRole("owner", "admin")(http.HandlerFunc(contactHandler.ExportContacts)))
	mux.HandleFunc("GET /api/v1/contacts/{id}", contactHandler.GetContact)
	mux.HandleFunc("GET /api/v1/contacts", contactHandler.ListContacts)
	mux.HandleFunc("POST /api/v1/contacts", contactHandler.CreateContact)

	// Audience Tags & Segmentation Endpoints
	mux.HandleFunc("GET /api/v1/tags", tagHandler.ListTags)
	mux.HandleFunc("POST /api/v1/tags", tagHandler.CreateTag)
	mux.Handle("PUT /api/v1/tags/{id}", handler.RequireRole("owner", "admin")(http.HandlerFunc(tagHandler.UpdateTag)))
	mux.Handle("DELETE /api/v1/tags/{id}", handler.RequireRole("owner", "admin")(http.HandlerFunc(tagHandler.DeleteTag)))
	mux.HandleFunc("POST /api/v1/contacts/{id}/tags", tagHandler.TagContact)
	mux.HandleFunc("DELETE /api/v1/contacts/{id}/tags/{tag_id}", tagHandler.UntagContact)
	mux.Handle("POST /api/v1/tags/{id}/bulk-assign", handler.RequireRole("owner", "admin")(http.HandlerFunc(tagHandler.BulkTagContacts)))

	// Message Templates Subsystem Endpoints
	mux.HandleFunc("GET /api/v1/templates", templateHandler.ListTemplates)
	mux.HandleFunc("POST /api/v1/templates", templateHandler.CreateTemplate)
	mux.HandleFunc("GET /api/v1/templates/{id}", templateHandler.GetTemplate)
	mux.HandleFunc("PUT /api/v1/templates/{id}", templateHandler.UpdateTemplate)
	mux.Handle("DELETE /api/v1/templates/{id}", handler.RequireRole("owner", "admin")(http.HandlerFunc(templateHandler.DeleteTemplate)))

	// Campaign Execution Subsystem Endpoints
	mux.HandleFunc("POST /api/v1/campaigns", campaignHandler.CreateCampaign)
	mux.HandleFunc("GET /api/v1/campaigns", campaignHandler.ListCampaigns)
	mux.HandleFunc("GET /api/v1/campaigns/{id}", campaignHandler.GetCampaign)
	mux.HandleFunc("POST /api/v1/campaigns/{id}/dispatch", campaignHandler.DispatchCampaign)
	mux.HandleFunc("POST /api/v1/campaigns/{id}/schedule", campaignHandler.ScheduleCampaign)
	mux.HandleFunc("DELETE /api/v1/campaigns/{id}/schedule", campaignHandler.CancelScheduledCampaign)
	mux.HandleFunc("POST /api/v1/campaigns/{id}/submit", campaignHandler.SubmitForApproval)
	mux.Handle("POST /api/v1/campaigns/{id}/approve", handler.RequireRole("owner", "admin")(http.HandlerFunc(campaignHandler.ApproveCampaign)))
	mux.Handle("POST /api/v1/campaigns/{id}/reject", handler.RequireRole("owner", "admin")(http.HandlerFunc(campaignHandler.RejectCampaign)))
	mux.HandleFunc("GET /api/v1/campaigns/{id}/stats", campaignHandler.GetCampaignStats)
	mux.HandleFunc("GET /api/v1/campaigns/{id}/report", campaignHandler.GetCampaignReport)
	mux.Handle("GET /api/v1/campaigns/{id}/export", handler.RequireRole("owner", "admin")(http.HandlerFunc(campaignHandler.ExportCampaignReport)))
	mux.HandleFunc("GET /api/v1/campaigns/{id}/deliveries", campaignHandler.GetCampaignDeliveries)
	mux.HandleFunc("GET /api/v1/ws/campaigns/{id}", campaignHub.HandleWebSocket)

	// Media Asset Subsystem Endpoints (Cloudinary CDN)
	mux.HandleFunc("POST /api/v1/media/upload", mediaHandler.UploadImage)

	// Dashboard Subsystem Endpoints
	mux.HandleFunc("GET /api/v1/dashboard/stats", dashboardHandler.GetStats)
	mux.Handle("GET /api/v1/deliveries/export", handler.RequireRole("owner", "admin")(http.HandlerFunc(dashboardHandler.ExportDeliveries)))
	mux.HandleFunc("GET /api/v1/deliveries", dashboardHandler.ListDeliveries)

	// Notification Center Endpoints
	mux.HandleFunc("GET /api/v1/notifications", notificationHandler.List)
	mux.HandleFunc("PATCH /api/v1/notifications/{id}/read", notificationHandler.MarkRead)
	mux.HandleFunc("PATCH /api/v1/notifications/read-all", notificationHandler.MarkAllRead)

	// Campaign Analytics Aggregate Subsystem Endpoints
	mux.HandleFunc("GET /api/v1/analytics", analyticsHandler.GetReport)

	// Unified Team Inbox Endpoints
	mux.HandleFunc("GET /api/v1/inbox/conversations", inboxHandler.ListConversations)
	mux.HandleFunc("POST /api/v1/inbox/conversations/bulk-assign", inboxHandler.BulkAssignConversations)
	mux.HandleFunc("GET /api/v1/inbox/conversations/{id}", inboxHandler.GetConversation)
	mux.HandleFunc("GET /api/v1/inbox/conversations/{id}/messages", inboxHandler.ListMessages)
	mux.HandleFunc("POST /api/v1/inbox/conversations/{id}/reply", inboxHandler.SendReply)
	mux.HandleFunc("PATCH /api/v1/inbox/conversations/{id}", inboxHandler.UpdateConversation)
	mux.HandleFunc("PUT /api/v1/inbox/conversations/{id}", inboxHandler.UpdateConversation)
	mux.HandleFunc("POST /api/v1/inbox/conversations/{id}/read", inboxHandler.MarkAsRead)
	mux.HandleFunc("PATCH /api/v1/inbox/messages/{msgID}", inboxHandler.EditMessage)
	mux.HandleFunc("DELETE /api/v1/inbox/messages/{msgID}", inboxHandler.DeleteMessage)
	mux.HandleFunc("GET /api/v1/ws/inbox", inboxHub.HandleWebSocket)

	// AI Business Assistant Subsystem Endpoints
	mux.HandleFunc("GET /api/v1/ai-agent/config", aiAgentHandler.GetConfig)
	mux.Handle("PUT /api/v1/ai-agent/config", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.SaveConfig)))
	mux.Handle("POST /api/v1/ai-agent/toggle", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.ToggleGlobalAI)))
	mux.HandleFunc("GET /api/v1/ai-agent/knowledge", aiAgentHandler.ListKnowledgeItems)
	mux.Handle("POST /api/v1/ai-agent/knowledge", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.CreateKnowledgeItem)))
	mux.Handle("POST /api/v1/ai-agent/knowledge/ingest", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.IngestDocument)))
	mux.Handle("GET /api/v1/ai-agent/knowledge/ingest", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.ListKnowledgeIngestionJobs)))
	mux.Handle("GET /api/v1/ai-agent/knowledge/ingest/{id}", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.GetKnowledgeIngestionJob)))
	mux.Handle("POST /api/v1/ai-agent/knowledge/approve", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.ApproveKnowledgeItems)))
	mux.HandleFunc("POST /api/v1/ai-agent/simulate", aiAgentHandler.TestSimulate)
	mux.Handle("PUT /api/v1/ai-agent/knowledge/{id}", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.UpdateKnowledgeItem)))
	mux.Handle("DELETE /api/v1/ai-agent/knowledge/{id}", handler.RequireRole("owner", "admin")(http.HandlerFunc(aiAgentHandler.DeleteKnowledgeItem)))
	mux.HandleFunc("POST /api/v1/ai-agent/conversations/{id}/toggle", aiAgentHandler.ToggleConversationAI)

	// Webhook Subsystem Endpoints (Inbound Event Flywheel)
	mux.HandleFunc("POST /api/v1/webhooks/telegram/{tenant_id}", webhookHandler.HandleTelegram)
	mux.HandleFunc("GET /api/v1/webhooks/whatsapp/{tenant_id}", webhookHandler.VerifyWhatsApp)
	mux.HandleFunc("POST /api/v1/webhooks/whatsapp/{tenant_id}", webhookHandler.HandleWhatsApp)

	// Billing & Monetization Endpoints (Stripe & Paystack)
	mux.HandleFunc("GET /api/v1/billing/subscription", billingHandler.GetSubscription)
	mux.Handle("POST /api/v1/billing/checkout", handler.RequireRole("owner", "admin")(http.HandlerFunc(billingHandler.CreateCheckout)))
	mux.Handle("POST /api/v1/billing/portal", handler.RequireRole("owner", "admin")(http.HandlerFunc(billingHandler.CreateCustomerPortal)))
	mux.HandleFunc("POST /api/v1/billing/webhook/stripe", billingHandler.HandleStripeWebhook)
	mux.HandleFunc("POST /api/v1/billing/webhook/paystack", billingHandler.HandlePaystackWebhook)

	// CORS Setup — parse comma-separated origins from environment
	allowedOrigins := strings.Split(cfg.AllowedOrigins, ",")
	for i := range allowedOrigins {
		allowedOrigins[i] = strings.TrimSpace(allowedOrigins[i])
	}

	c := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
	})
	corsHandler := c.Handler(handler.AuthMiddleware(identityUseCase)(mux))
	loggedHandler := handler.RequestLoggerMiddleware(logger)(corsHandler)

	// 5. Configure Network Server Parameters
	srv := &http.Server{
		Addr:         cfg.Port,
		Handler:      loggedHandler,
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// 6. Execute Graceful Shutdown Orchestration
	shutdownErrorChan := make(chan error, 1)
	go func() {
		logger.Printf("API Gateway launching network runtime on %s\n", srv.Addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			shutdownErrorChan <- err
		}
	}()

	quitSignals := make(chan os.Signal, 1)
	signal.Notify(quitSignals, os.Interrupt, syscall.SIGTERM)
	sig := <-quitSignals
	logger.Printf("Termination signal received (%s). Commencing graceful cleanup drain loop...\n", sig.String())

	cancelWorkers()
	if broadcastWorker != nil {
		broadcastWorker.Stop()
	}
	if telemetryWorker != nil {
		telemetryWorker.Stop()
	}
	if waManager != nil {
		waManager.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatalf("Network listener forced hard collapse during shutdown: %v", err)
	}

	logger.Println("API Gateway instance safely spun down. Clean exit.")
}

func loadEnv(logger *log.Logger) {
	candidates := []string{
		filepath.Join(".", ".env"),
		filepath.Join("..", ".env"),
		filepath.Join("..", "..", ".env"),
		filepath.Join("..", "..", "..", ".env"),
	}
	for _, candidate := range candidates {
		if err := godotenv.Load(candidate); err == nil {
			logger.Printf("Loaded environment file from %s\n", candidate)
			return
		}
	}
	logger.Println("No .env file loaded; using process environment variables")
}
