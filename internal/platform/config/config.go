// Package config loads service configuration from environment variables only.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
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
}

// Load reads and validates configuration from the environment.
func Load() (Config, error) {
	c := Config{
		Store:       Store(getenv("STORE", string(StorePostgres))),
		DatabaseDSN: os.Getenv("DB_DSN"),
		JWTSecret:   []byte(os.Getenv("JWT_SECRET")),
		JWTIssuer:   os.Getenv("JWT_ISSUER"),
		LogLevel:    getenv("LOG_LEVEL", "info"),
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
	return c, errors.Join(errs...)
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
