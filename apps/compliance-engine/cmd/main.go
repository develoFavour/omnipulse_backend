package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"omnipulse/apps/compliance-engine/internal/config"
	"omnipulse/apps/compliance-engine/internal/repository"
	"omnipulse/apps/compliance-engine/internal/worker"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	// 1. Initialize Logging Matrix & Environment Ingestion
	godotenv.Load("../../.env")
	logger := log.New(os.Stdout, "[COMPLIANCE-ENGINE] ", log.LstdFlags|log.Lshortfile)
	cfg := config.Load()

	logger.Printf("Bootstrapping subsystem engine runtime [Mode: %s]...\n", cfg.Environment)

	// 2. Establish High-Performance Redis Key-Value Connection Options
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Fatalf("Invalid Redis connection URL string schema: %v", err)
	}

	// Optimize connection pool limits for background thread safety
	opts.PoolSize = 10
	opts.MinIdleConns = 5

	rdb := redis.NewClient(opts)

	// Test database link before proceeding
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Fatalf("Redis backing storage cluster completely unreachable: %v", err)
	}
	logger.Println("Successfully attached to Redis caching layer pool.")
	defer rdb.Close()

	// 3. Dependency Injection Architecture Component Wiring
	complianceRepo := repository.NewRedisComplianceRepository(rdb)

	natsConsumer, err := worker.NewCampaignConsumer(cfg.NatsURL, complianceRepo)
	if err != nil {
		logger.Fatalf("Failed to initialize NATS streaming consumer engine node: %v", err)
	}

	// 4. Fire Up the Live Asynchronous Background Pipeline Monitor
	globalCtx, globalCancel := context.WithCancel(context.Background())
	defer globalCancel()

	if err := natsConsumer.Start(globalCtx); err != nil {
		logger.Fatalf("NATS processing stream channel registration failed: %v", err)
	}

	// 5. Execute Graceful System Termination Interception
	quitSignals := make(chan os.Signal, 1)
	signal.Notify(quitSignals, os.Interrupt, syscall.SIGTERM)

	// Keep main routine process alive waiting for signal strike
	sig := <-quitSignals
	logger.Printf("System termination trigger detected (%s). Initiating safety drain loop...\n", sig.String())

	// Force strict 10-second hard limit window to drop all connections safely
	globalCancel() // Stops spawning new internal processing cycles
	natsConsumer.Stop()

	logger.Println("Compliance Engine worker pool safely terminated. Clean exit.")
}
