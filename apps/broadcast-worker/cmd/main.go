package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"omnipulse/apps/broadcast-worker/internal/config"
	"omnipulse/apps/broadcast-worker/internal/worker"

	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load("../../.env")
	logger := log.New(os.Stdout, "[BROADCAST-WORKER] ", log.LstdFlags)
	cfg := config.Load()

	logger.Printf("Launching broadcast delivery engine node [Mode: %s]...\n", cfg.Environment)

	broadcastConsumer, err := worker.NewBroadcastConsumer(cfg.NatsURL)
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
