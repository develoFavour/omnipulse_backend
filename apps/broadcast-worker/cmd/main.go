package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"omnipulse/apps/broadcast-worker/internal/config"
	"omnipulse/apps/broadcast-worker/internal/worker"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	logger := log.New(os.Stdout, "[BROADCAST-WORKER] ", log.LstdFlags)
	loadEnv(logger)
	cfg := config.Load()

	logger.Printf("Launching broadcast delivery engine node [Mode: %s]...\n", cfg.Environment)

	// Initialize Database Connection
	db, err := sql.Open("postgres", cfg.DatabaseURL)
	if err != nil {
		logger.Fatalf("Failed to initialize database connection: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		logger.Fatalf("Database ping failed: %v", err)
	}
	logger.Println("Database connection established successfully")

	broadcastConsumer, err := worker.NewBroadcastConsumer(cfg.NatsURL, db)
	if err != nil {
		logger.Fatalf("Failed to establish NATS streaming consumer link: %v", err)
	}

	globalCtx, globalCancel := context.WithCancel(context.Background())
	defer globalCancel()

	if err := broadcastConsumer.Start(globalCtx); err != nil {
		logger.Fatalf("Outbound subscription allocation failed: %v", err)
	}

	quitSignals := make(chan os.Signal, 1)
	signal.Notify(quitSignals, os.Interrupt, syscall.SIGTERM)

	sig := <-quitSignals
	logger.Printf("Shutdown code captured (%s). Cleaning delivery connection pipelines...\n", sig.String())

	globalCancel()
	broadcastConsumer.Stop()

	logger.Println("Broadcast Engine gracefully spun down. Clear pipeline exit.")
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
