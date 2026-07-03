package config

import (
	"os"
	"strconv"
)

// Config holds all operational parameters for the API Gateway
type Config struct {
	Port        string
	DatabaseURL string
	Environment string
	DBMaxConns  int
	NatsURL     string
}

// Load reads values from the OS environment variables or supplies secure defaults
func Load() *Config {
	return &Config{
		Port:        getEnv("PORT", ":8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://admin:secretpassword@localhost:5432/omnipulse_dev?sslmode=disable"),
		Environment: getEnv("APP_ENV", "development"),
		DBMaxConns:  getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
		NatsURL:     getEnv("NATS_URL", "nats://localhost:4222"),
	}
}

// Helper to read string env or fallback
func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// Helper to read integer env or fallback
func getEnvAsInt(key string, fallback int) int {
	valueStr := getEnv(key, "")
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return fallback
}
