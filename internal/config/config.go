package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port         string
	DatabaseDSN  string
	KeyExpiryTTL time.Duration
}

func Load() Config {
	return Config{
		Port:         envOrDefault("PORT", "8080"),
		DatabaseDSN:  envOrDefault("DATABASE_DSN", "postgres://postgres@localhost:5432/idempotency?sslmode=disable"),
		KeyExpiryTTL: parseDurationHours(envOrDefault("KEY_EXPIRY_HOURS", "24")),
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDurationHours(s string) time.Duration {
	h, err := strconv.Atoi(s)
	if err != nil {
		h = 24
	}
	return time.Duration(h) * time.Hour
}
