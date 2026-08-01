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

	// 2. Initialize NATS JetStream Event Broker Adapter
	natsPublisher, err := event.NewJetStreamPublisher(cfg.NatsURL, cfg.NatsCreds)
	if err != nil {
		logger.Fatalf("Failed to initialize NATS streaming fabric core: %v", err)
	}
	logger.Println("Successfully connected to NATS JetStream fabric.")

	// 3. Dependency Injection Architecture Wiring
	contactRepo := repository.NewPostgresContactRepository(db)
	campaignRepo := repository.NewPostgresCampaignRepository(db)
	identityRepo := repository.NewPostgresIdentityRepository(db)
	channelRepo := repository.NewPostgresChannelRepository(db)
	dashboardRepo := repository.NewPostgresDashboardRepository(db)
	destinationRepo := repository.NewPostgresTelegramDestinationRepository(db)

	contactUseCase := usecase.NewContactUseCase(contactRepo)
	campaignUseCase := usecase.NewCampaignUseCase(campaignRepo, contactRepo, destinationRepo, natsPublisher)
	identityUseCase := usecase.NewIdentityUseCase(identityRepo, channelRepo)
	dashboardUseCase := usecase.NewDashboardUseCase(dashboardRepo)

	contactHandler := handler.NewContactHandler(contactUseCase)
	campaignHandler := handler.NewCampaignHandler(campaignUseCase)
	identityHandler := handler.NewIdentityHandler(identityUseCase)
	channelHandler := handler.NewChannelHandler(channelRepo, cfg.PublicAPIBaseURL, cfg.PublicAppBaseURL, handler.MetaAppConfig{
		AppID:        cfg.MetaAppID,
		AppSecret:    cfg.MetaAppSecret,
		WABAConfigID: cfg.MetaWABAConfigID,
	})
	webhookHandler := handler.NewWebhookHandler(contactUseCase, channelRepo, destinationRepo)
	dashboardHandler := handler.NewDashboardHandler(dashboardUseCase)
	destinationHandler := handler.NewTelegramDestinationHandler(destinationRepo)

	// Initialize the background Telemetry Audit Listener Component
	globalWorkerCtx, cancelWorkers := context.WithCancel(context.Background())
	var natsConn *nats.Conn
	var natsJS nats.JetStreamContext
	if jp, ok := natsPublisher.(*event.JetStreamPublisher); ok {
		natsConn, natsJS = jp.GetConn()
	}

	telemetryWorker, err := worker.NewTelemetryConsumer(cfg.NatsURL, cfg.NatsCreds, natsConn, natsJS, campaignRepo)
	if err != nil {
		logger.Printf("[NATS-WARN] Telemetry worker initialization deferred: %v\n", err)
	} else {
		if err := telemetryWorker.Start(globalWorkerCtx); err != nil {
			logger.Printf("[NATS-WARN] Telemetry stream subscription deferred: %v\n", err)
		} else {
			defer telemetryWorker.Stop()
		}
	}

	// 4. Modern Native HTTP Routing Multiplexer
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "healthy", "service": "api-gateway"})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		utils.WriteJSON(w, http.StatusOK, map[string]string{"status": "healthy", "service": "api-gateway"})
	})

	// Identity Subsystem Endpoints
	mux.HandleFunc("POST /api/v1/auth/sync", identityHandler.SyncUser)
	mux.HandleFunc("PATCH /api/v1/onboarding/brand", identityHandler.UpdateBrand)
	mux.HandleFunc("POST /api/v1/onboarding/complete", identityHandler.CompleteOnboarding)

	// Channel Subsystem Endpoints
	mux.HandleFunc("POST /api/v1/channels", channelHandler.CreateChannel)
	mux.HandleFunc("GET /api/v1/channels", channelHandler.ListChannels)
	mux.HandleFunc("DELETE /api/v1/channels/{platform}", channelHandler.HandleDisconnectChannel)

	// WhatsApp Embedded Signup (1-Click OAuth) Endpoints
	mux.HandleFunc("GET /api/v1/channels/whatsapp/oauth/config", channelHandler.HandleWhatsAppOAuthConfig)
	mux.HandleFunc("POST /api/v1/channels/whatsapp/oauth/callback", channelHandler.HandleWhatsAppOAuthCallback)

	// Telegram Destination Endpoints
	mux.HandleFunc("GET /api/v1/telegram/destinations", destinationHandler.ListDestinations)

	// Contact Subsystem Endpoints
	mux.HandleFunc("GET /api/v1/contacts/{id}", contactHandler.GetContact)
	mux.HandleFunc("GET /api/v1/contacts", contactHandler.ListContacts)
	mux.HandleFunc("POST /api/v1/contacts", contactHandler.CreateContact)

	// Campaign Execution Subsystem Endpoints
	mux.HandleFunc("POST /api/v1/campaigns", campaignHandler.CreateCampaign)
	mux.HandleFunc("GET /api/v1/campaigns", campaignHandler.ListCampaigns)
	mux.HandleFunc("POST /api/v1/campaigns/{id}/dispatch", campaignHandler.DispatchCampaign)
	mux.HandleFunc("GET /api/v1/campaigns/{id}/stats", campaignHandler.GetCampaignStats)

	// Dashboard Subsystem Endpoints
	mux.HandleFunc("GET /api/v1/dashboard/stats", dashboardHandler.GetStats)
	mux.HandleFunc("GET /api/v1/deliveries", dashboardHandler.ListDeliveries)

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
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
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
	if telemetryWorker != nil {
		telemetryWorker.Stop()
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
