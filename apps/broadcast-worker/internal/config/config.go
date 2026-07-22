package config

import (
	"os"
)

type Config struct {
	NatsURL     string
	Environment string
	DatabaseURL string
}

func Load() *Config {
	return &Config{
		NatsURL:     getEnv("NATS_URL", "nats://localhost:4222"),
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
