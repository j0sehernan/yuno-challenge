package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// Ensure env vars are unset for this test
	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_DSN")
	os.Unsetenv("KEY_EXPIRY_HOURS")

	cfg := Load()

	if cfg.Port != "8080" {
		t.Errorf("expected port 8080, got %s", cfg.Port)
	}
	if cfg.DatabaseDSN != "postgres://postgres@localhost:5432/idempotency?sslmode=disable" {
		t.Errorf("unexpected DSN: %s", cfg.DatabaseDSN)
	}
	if cfg.KeyExpiryTTL != 24*time.Hour {
		t.Errorf("expected 24h TTL, got %v", cfg.KeyExpiryTTL)
	}
}

func TestLoad_CustomEnv(t *testing.T) {
	os.Setenv("PORT", "9090")
	os.Setenv("DATABASE_DSN", "postgres://custom@db:5432/test?sslmode=disable")
	os.Setenv("KEY_EXPIRY_HOURS", "48")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("DATABASE_DSN")
		os.Unsetenv("KEY_EXPIRY_HOURS")
	}()

	cfg := Load()

	if cfg.Port != "9090" {
		t.Errorf("expected port 9090, got %s", cfg.Port)
	}
	if cfg.DatabaseDSN != "postgres://custom@db:5432/test?sslmode=disable" {
		t.Errorf("unexpected DSN: %s", cfg.DatabaseDSN)
	}
	if cfg.KeyExpiryTTL != 48*time.Hour {
		t.Errorf("expected 48h TTL, got %v", cfg.KeyExpiryTTL)
	}
}

func TestParseDurationHours_Invalid(t *testing.T) {
	d := parseDurationHours("not-a-number")
	if d != 24*time.Hour {
		t.Errorf("expected 24h fallback, got %v", d)
	}
}

func TestEnvOrDefault(t *testing.T) {
	os.Unsetenv("TEST_KEY_NONEXISTENT")
	v := envOrDefault("TEST_KEY_NONEXISTENT", "fallback")
	if v != "fallback" {
		t.Errorf("expected fallback, got %s", v)
	}

	os.Setenv("TEST_KEY_EXISTS", "custom")
	defer os.Unsetenv("TEST_KEY_EXISTS")
	v = envOrDefault("TEST_KEY_EXISTS", "fallback")
	if v != "custom" {
		t.Errorf("expected custom, got %s", v)
	}
}
