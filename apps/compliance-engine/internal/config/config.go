package config

import (
	"os"
)

// Config stores the system endpoints for our background worker
type Config struct {
	NatsURL     string
	RedisURL    string
	Environment string
}

// Load reads variables from the host system environment or falls back to local docker configurations
func Load() *Config {
	return &Config{
		NatsURL:     getEnv("NATS_URL", "nats://localhost:4222"),
		RedisURL:    getEnv("REDIS_URL", "redis://localhost:6379/0"),
		Environment: getEnv("APP_ENV", "development"),
	}
}

// Safe abstraction helper to read an OS environment variable or use a developer fallback
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
