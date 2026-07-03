package main

import (
	"context"
	"log"
	"os"
	"time"

	"omnipulse/apps/compliance-engine/internal/config"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

func main() {
	godotenv.Load("../../.env")
	logger := log.New(os.Stdout, "[REDIS-SEED] ", log.LstdFlags)
	cfg := config.Load()

	logger.Printf("Connecting to Redis target cluster: %s\n", cfg.RedisURL)

	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Fatalf("Invalid Redis string schema: %v", err)
	}

	rdb := redis.NewClient(opts)
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Flush any existing testing state to ensure pristine tracking runs
	logger.Println("Clearing previous compliance cache fields...")

	// We scan for keys matching our specific namespace structure to avoid wiping other operational keys
	var cursor uint64
	for {
		keys, nextCursor, err := rdb.Scan(ctx, cursor, "compliance:*", 100).Result()
		if err != nil {
			logger.Fatalf("Failed to scan old cache keys: %v", err)
		}

		if len(keys) > 0 {
			if err := rdb.Del(ctx, keys...).Err(); err != nil {
				logger.Fatalf("Failed to purge target keys: %v", err)
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	// 2. Inject explicit mock opt-out records matching our Postgres Dev Seeds!
	// We will explicitly block Malicious Spammer (WhatsApp) and Bob Jones (WhatsApp)
	optOuts := map[string]string{
		"compliance:whatsapp:+15105559999": "true", // Malicious Spammer's WhatsApp number
		"compliance:whatsapp:+14155552671": "true", // Bob Jones's WhatsApp number
	}

	logger.Println("Injecting mock customer opt-out tokens...")
	for key, value := range optOuts {
		err := rdb.Set(ctx, key, value, 0).Err() // 0 means no expiration; permanent block
		if err != nil {
			logger.Fatalf("Failed to write key %s: %v", key, err)
		}
		logger.Printf("Successfully blacklisted target: [ %s ]\n", key)
	}

	logger.Println("🚀 Redis compliance dataset seeding complete! Ready for evaluation testing.")
}
