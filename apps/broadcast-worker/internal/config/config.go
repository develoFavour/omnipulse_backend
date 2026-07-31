package config

import (
	"os"
)

type Config struct {
	NatsURL     string
	NatsCreds   string
	Environment string
	DatabaseURL string
}

func Load() *Config {
	return &Config{
		NatsURL:     getEnv("NATS_URL", "nats://localhost:4222"),
		NatsCreds:   getEnv("NATS_CREDS", ""),
		Environment: getEnv("APP_ENV", "development"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/omnipulse?sslmode=disable"),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}
