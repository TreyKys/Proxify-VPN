// Package config loads control-plane configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr            string
	DatabaseURL     string
	JWTSecret       []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	PaystackSecretKey string
	// Paystack signs webhooks with the secret key; kept separate so a
	// test/live key swap doesn't silently break signature checks.
	PaystackWebhookKey string

	// ReconcileInterval is how often the peer reconciler sweeps for work that
	// the request path failed to apply inline.
	ReconcileInterval time.Duration
	// EdgeTimeout bounds a single call to an edge agent. Edge boxes are on
	// cheap hosts with occasionally bad routes; we fail fast and retry rather
	// than hold a user's provisioning request open.
	EdgeTimeout time.Duration

	// FreePlanEnabled grants the free data-capped plan on signup.
	FreePlanEnabled bool

	Env string
}

func Load() (Config, error) {
	c := Config{
		Addr:               env("PROXIFY_ADDR", ":8080"),
		DatabaseURL:        env("DATABASE_URL", "postgres://proxify:proxify@localhost:5432/proxify?sslmode=disable"),
		AccessTokenTTL:     envDuration("PROXIFY_ACCESS_TTL", 30*time.Minute),
		RefreshTokenTTL:    envDuration("PROXIFY_REFRESH_TTL", 90*24*time.Hour),
		PaystackSecretKey:  os.Getenv("PAYSTACK_SECRET_KEY"),
		PaystackWebhookKey: os.Getenv("PAYSTACK_WEBHOOK_KEY"),
		ReconcileInterval:  envDuration("PROXIFY_RECONCILE_INTERVAL", 20*time.Second),
		EdgeTimeout:        envDuration("PROXIFY_EDGE_TIMEOUT", 5*time.Second),
		FreePlanEnabled:    envBool("PROXIFY_FREE_PLAN", true),
		Env:                env("PROXIFY_ENV", "dev"),
	}

	secret := os.Getenv("PROXIFY_JWT_SECRET")
	if secret == "" {
		if c.Env != "dev" {
			return Config{}, fmt.Errorf("PROXIFY_JWT_SECRET is required outside dev")
		}
		secret = "dev-only-insecure-secret-do-not-ship"
	}
	if len(secret) < 32 && c.Env != "dev" {
		return Config{}, fmt.Errorf("PROXIFY_JWT_SECRET must be at least 32 bytes")
	}
	c.JWTSecret = []byte(secret)

	if c.PaystackWebhookKey == "" {
		c.PaystackWebhookKey = c.PaystackSecretKey
	}
	if c.Env != "dev" && c.PaystackSecretKey == "" {
		return Config{}, fmt.Errorf("PAYSTACK_SECRET_KEY is required outside dev")
	}
	return c, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
