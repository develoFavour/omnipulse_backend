package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"omnipulse/apps/api-gateway/internals/config"
	"omnipulse/apps/api-gateway/internals/utils"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

func main() {
	logger := log.New(os.Stdout, "[API-GATEWAY] ", log.LstdFlags|log.Lshortfile)

	// 1. Instantly ingest secure environment configuration configurations
	cfg := config.Load()

	// 2. Establish Database Connection Pool via Configuration Strategy
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		logger.Fatalf("Database connection initialization failed: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(cfg.DBMaxConns)
	db.SetMaxIdleConns(cfg.DBMaxConns)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		logger.Fatalf("Database cluster unreachable: %v", err)
	}
	logger.Printf("Successfully attached to PostgreSQL database pool [Mode: %s].\n", cfg.Environment)

	// 3. Modern Native HTTP Routing Multiplexer
	mux := http.NewServeMux()

	// Base routing health checks using our new standardized JSON utils
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		healthStatus := map[string]string{
			"status":  "healthy",
			"service": "api-gateway",
			"env":     cfg.Environment,
		}
		utils.WriteJSON(w, http.StatusOK, healthStatus)
	})

	// 4. Configure Network Server Parameters using configuration properties
	srv := &http.Server{
		Addr:         cfg.Port,
		Handler:      mux,
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	// 5. Execute Graceful Shutdown Orchestration
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatalf("Network listener forced hard collapse during shutdown: %v", err)
	}

	logger.Println("API Gateway instance safely spun down. Clean exit.")
}
