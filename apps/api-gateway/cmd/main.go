package main

import (
	"context"
	"database/sql"
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

	// 2. Initialize NATS JetStream Event Broker Adapter
	natsPublisher, err := event.NewJetStreamPublisher(cfg.NatsURL, cfg.NatsCreds)
	if err != nil {
		logger.Fatalf("Failed to initialize NATS streaming fabric core: %v", err)
	}
	logger.Println("Successfully connected to NATS JetStream fabric.")

	contactRepo := repository.NewPostgresContactRepository(db)
	campaignRepo := repository.NewPostgresCampaignRepository(db)
	identityRepo := repository.NewPostgresIdentityRepository(db)
	channelRepo := repository.NewPostgresChannelRepository(db)
	dashboardRepo := repository.NewPostgresDashboardRepository(db)
	destinationRepo := repository.NewPostgresTelegramDestinationRepository(db)
	tagRepo := repository.NewPostgresTagRepository(db)
	templateRepo := repository.NewPostgresTemplateRepository(db)
	notificationRepo := repository.NewPostgresNotificationRepository(db)
	analyticsRepo := repository.NewPostgresAnalyticsRepository(db)

	tagUseCase := usecase.NewTagUseCase(tagRepo)
	templateUseCase := usecase.NewTemplateUseCase(templateRepo)
	contactUseCase := usecase.NewContactUseCase(contactRepo, tagRepo)
	campaignUseCase := usecase.NewCampaignUseCase(campaignRepo, contactRepo, destinationRepo, natsPublisher)
	identityUseCase := usecase.NewIdentityUseCase(identityRepo, channelRepo)
	dashboardUseCase := usecase.NewDashboardUseCase(dashboardRepo)
	notificationUseCase := usecase.NewNotificationUseCase(notificationRepo)
	analyticsUseCase := usecase.NewAnalyticsUseCase(analyticsRepo)

	tagHandler := handler.NewTagHandler(tagUseCase)
	templateHandler := handler.NewTemplateHandler(templateUseCase)
	contactHandler := handler.NewContactHandler(contactUseCase)
	campaignHandler := handler.NewCampaignHandler(campaignUseCase)
	identityHandler := handler.NewIdentityHandler(identityUseCase)
	settingsHandler := handler.NewSettingsHandler(db)
	notificationHandler := handler.NewNotificationHandler(notificationUseCase)
	analyticsHandler := handler.NewAnalyticsHandler(analyticsUseCase)

	mailer := utils.NewMailer(cfg)
	teamUseCase := usecase.NewTeamUseCase(identityRepo, mailer, cfg.PublicAppBaseURL, logger)
	teamHandler := handler.NewTeamHandler(teamUseCase)

	var waManager *service.WhatsAppManager
	var waErr error
	waManager, waErr = service.NewWhatsAppManager(db)
	if waErr != nil {
		logger.Printf("[WA-WARN] WhatsApp Multi-Device manager initialization deferred: %v\n", waErr)
	} else {
		logger.Println("Successfully initialized WhatsApp Multi-Device session manager.")
	}

	channelHandler := handler.NewChannelHandler(channelRepo, contactRepo, cfg.PublicAPIBaseURL, cfg.PublicAppBaseURL, handler.MetaAppConfig{
		AppID:         cfg.MetaAppID,
		AppSecret:     cfg.MetaAppSecret,
		WABAConfigID:  cfg.MetaWABAConfigID,
		WABAID:        cfg.MetaWABAID,
		PhoneNumberID: cfg.MetaPhoneNumberID,
	}, waManager)
	webhookHandler := handler.NewWebhookHandler(contactUseCase, channelRepo, destinationRepo)
	dashboardHandler := handler.NewDashboardHandler(dashboardUseCase)
	destinationHandler := handler.NewTelegramDestinationHandler(destinationRepo)

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
	bw, err := worker.NewBroadcastConsumer(natsConn, natsJS, db, waManager)
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
	mux.HandleFunc("GET /api/v1/campaigns/{id}/stats", campaignHandler.GetCampaignStats)
	mux.HandleFunc("GET /api/v1/campaigns/{id}/deliveries", campaignHandler.GetCampaignDeliveries)
	mux.HandleFunc("GET /api/v1/ws/campaigns/{id}", campaignHub.HandleWebSocket)

	// Media Asset Subsystem Endpoints (Cloudinary CDN)
	mux.HandleFunc("POST /api/v1/media/upload", mediaHandler.UploadImage)

	// Dashboard Subsystem Endpoints
	mux.HandleFunc("GET /api/v1/dashboard/stats", dashboardHandler.GetStats)
	mux.HandleFunc("GET /api/v1/deliveries", dashboardHandler.ListDeliveries)

	// Notification Center Endpoints
	mux.HandleFunc("GET /api/v1/notifications", notificationHandler.List)
	mux.HandleFunc("PATCH /api/v1/notifications/{id}/read", notificationHandler.MarkRead)
	mux.HandleFunc("PATCH /api/v1/notifications/read-all", notificationHandler.MarkAllRead)

	// Campaign Analytics Aggregate Subsystem Endpoints
	mux.HandleFunc("GET /api/v1/analytics", analyticsHandler.GetReport)

	// Webhook Subsystem Endpoints (Inbound Event Flywheel)
	mux.HandleFunc("POST /api/v1/webhooks/telegram/{tenant_id}", webhookHandler.HandleTelegram)
	mux.HandleFunc("GET /api/v1/webhooks/whatsapp/{tenant_id}", webhookHandler.VerifyWhatsApp)
	mux.HandleFunc("POST /api/v1/webhooks/whatsapp/{tenant_id}", webhookHandler.HandleWhatsApp)

	// CORS Setup — parse comma-separated origins from environment
	allowedOrigins := strings.Split(cfg.AllowedOrigins, ",")
	for i := range allowedOrigins {
		allowedOrigins[i] = strings.TrimSpace(allowedOrigins[i])
	}

	c := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
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
