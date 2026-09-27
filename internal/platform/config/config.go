// Package config loads service configuration from environment variables only.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Store selects the outbound persistence adapter.
type Store string

// Supported stores.
const (
	StorePostgres Store = "postgres"
	StoreMemory   Store = "memory" // local demo only; data is lost on restart
)

// Config is the runtime configuration. Secrets never have defaults.
type Config struct {
	Port           int
	Store          Store
	DatabaseDSN    string
	JWTSecret      []byte
	JWTIssuer      string
	LogLevel       string
	RequestTimeout time.Duration
	// 2C2P merchant credentials for webhook verification.
	TwoC2PMerchantID string
	TwoC2PSecretKey  []byte
	// Kafka bootstrap brokers for the outbox relay; empty disables publishing to Kafka.
	KafkaBrokers       []string
	OutboxPollInterval time.Duration
}

// Load reads and validates configuration from the environment.
func Load() (Config, error) {
	c := Config{
		Store:       Store(getenv("STORE", string(StorePostgres))),
		DatabaseDSN: os.Getenv("DB_DSN"),
		JWTSecret:   []byte(os.Getenv("JWT_SECRET")),
		JWTIssuer:   os.Getenv("JWT_ISSUER"),
		LogLevel:    getenv("LOG_LEVEL", "info"),

		TwoC2PMerchantID: os.Getenv("TWOC2P_MERCHANT_ID"),
		TwoC2PSecretKey:  []byte(os.Getenv("TWOC2P_SECRET_KEY")),

		KafkaBrokers: splitList(os.Getenv("KAFKA_BROKERS")),
	}
	var errs []error
	port, err := strconv.Atoi(getenv("APP_PORT", "8080"))
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, errors.New("APP_PORT must be 1-65535"))
	}
	c.Port = port
	c.RequestTimeout, err = time.ParseDuration(getenv("REQUEST_TIMEOUT", "5s"))
	if err != nil || c.RequestTimeout <= 0 {
		errs = append(errs, errors.New("REQUEST_TIMEOUT must be a positive duration"))
	}
	c.OutboxPollInterval, err = time.ParseDuration(getenv("OUTBOX_POLL_INTERVAL", "1s"))
	if err != nil || c.OutboxPollInterval <= 0 {
		errs = append(errs, errors.New("OUTBOX_POLL_INTERVAL must be a positive duration"))
	}
	switch c.Store {
	case StorePostgres:
		if c.DatabaseDSN == "" {
			errs = append(errs, errors.New("DB_DSN is required when STORE=postgres"))
		}
	case StoreMemory:
	default:
		errs = append(errs, fmt.Errorf("STORE must be %q or %q", StorePostgres, StoreMemory))
	}
	if len(c.JWTSecret) < 32 {
		errs = append(errs, errors.New("JWT_SECRET must be at least 32 bytes"))
	}
	if c.JWTIssuer == "" {
		errs = append(errs, errors.New("JWT_ISSUER is required"))
	}
	if c.TwoC2PMerchantID == "" {
		errs = append(errs, errors.New("TWOC2P_MERCHANT_ID is required"))
	}
	if len(c.TwoC2PSecretKey) < 32 {
		errs = append(errs, errors.New("TWOC2P_SECRET_KEY must be at least 32 bytes"))
	}
	return c, errors.Join(errs...)
}

// splitList parses a comma-separated list, ignoring blanks.
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
