// Package config owns all gateway runtime configuration.
//
// It deliberately reads environment variables once at startup. Adapters and
// storage receive the parsed values they need instead of reading environment
// variables during a request.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultListenAddress            = ":8090"
	DefaultRequestTimeout           = 620 * time.Second
	DefaultStoreLogTimeout          = 5 * time.Second
	DefaultPostgresDSN              = "host=localhost port=5432 user=arxiv password=arxiv_secret dbname=arxiv_db sslmode=disable"
	DefaultArxivServiceURL          = "http://localhost:8080"
	DefaultArxivTimeout             = 600 * time.Second
	DefaultArxivMaxResponseBytes    = 4 << 20
	DefaultEvaluatorServiceURL      = "http://localhost:8002"
	DefaultEvaluatorTimeout         = 120 * time.Second
	DefaultEvaluatorMaxResponseSize = 1 << 20
	DefaultRAGServiceURL            = "http://rag-api:8000"
	// 60s allows the RAG service's generation and upstream retry budget to finish.
	DefaultRAGTimeout          = 60 * time.Second
	DefaultRAGMaxResponseBytes = 4 << 20

	// Rate limiting defaults
	DefaultRateLimitRequestsPerMinute = 60
	DefaultRateLimitBurst             = 10

	// Postgres pool defaults
	DefaultPostgresMaxOpenConns    = 25
	DefaultPostgresMaxIdleConns    = 5
	DefaultPostgresConnMaxLifetime = 5 * time.Minute
	DefaultPostgresConnMaxIdleTime = 1 * time.Minute
)

// ServiceConfig is the runtime configuration shared by a downstream HTTP
// service. MaxResponseBytes is enforced by its adapter.
type ServiceConfig struct {
	BaseURL          string
	Timeout          time.Duration
	MaxResponseBytes int64
}

// Config contains the complete runtime configuration used by the gateway.
type Config struct {
	ListenAddress   string
	RequestTimeout  time.Duration
	StoreLogTimeout time.Duration
	PostgresDSN     string
	PostgresPool    PostgresPoolConfig
	Arxiv           ServiceConfig
	Evaluator       ServiceConfig
	RAG             ServiceConfig
	// RateLimit is the rate limiting configuration for public endpoints.
	RateLimit RateLimitConfig
	// Auth is the API key authentication configuration.
	Auth AuthConfig
}

// PostgresPoolConfig controls connection pooling for PostgreSQL.
type PostgresPoolConfig struct {
	MaxOpenConns    int64
	MaxIdleConns    int64
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// RateLimitConfig controls token-bucket rate limiting for HTTP endpoints.
type RateLimitConfig struct {
	// RequestsPerMinute is the steady-state request rate allowed per key.
	RequestsPerMinute int64
	// Burst is the maximum burst size allowed before rate limiting kicks in.
	Burst int64
}

// AuthConfig controls API key authentication for HTTP endpoints.
// If APIKeys is empty, authentication is disabled (dev mode).
type AuthConfig struct {
	// APIKeys is a comma-separated list of valid API keys.
	APIKeys []string
	// KeyTiers maps API key → tier name (e.g., "pro", "enterprise").
	// Format in env: "key1:pro,key2:enterprise"
	KeyTiers map[string]string
	// HeaderName is the header used to transmit the API key (default: "Authorization").
	HeaderName string
	// HeaderPrefix is the prefix expected before the key (default: "Bearer").
	HeaderPrefix string
	// AdminKey is the key required to authorize key rotation. Empty = no admin protection.
	AdminKey string
}

// Default returns the documented local-development values without reading the
// environment. It is also useful for handler tests that do not need runtime
// configuration overrides.
func Default() Config {
	return Config{
		ListenAddress:   DefaultListenAddress,
		RequestTimeout:  DefaultRequestTimeout,
		StoreLogTimeout: DefaultStoreLogTimeout,
		PostgresDSN:     DefaultPostgresDSN,
		PostgresPool: PostgresPoolConfig{
			MaxOpenConns:    int64(DefaultPostgresMaxOpenConns),
			MaxIdleConns:    int64(DefaultPostgresMaxIdleConns),
			ConnMaxLifetime: DefaultPostgresConnMaxLifetime,
			ConnMaxIdleTime: DefaultPostgresConnMaxIdleTime,
		},
		RateLimit: RateLimitConfig{
			RequestsPerMinute: DefaultRateLimitRequestsPerMinute,
			Burst:             DefaultRateLimitBurst,
		},
		Auth: AuthConfig{
			HeaderName:    "Authorization",
			HeaderPrefix:  "Bearer",
			AdminKey:      "",
			KeyTiers:      nil,
		},
		Arxiv: ServiceConfig{
			BaseURL:          DefaultArxivServiceURL,
			Timeout:          DefaultArxivTimeout,
			MaxResponseBytes: DefaultArxivMaxResponseBytes,
		},
		Evaluator: ServiceConfig{
			BaseURL:          DefaultEvaluatorServiceURL,
			Timeout:          DefaultEvaluatorTimeout,
			MaxResponseBytes: DefaultEvaluatorMaxResponseSize,
		},
		RAG: ServiceConfig{
			BaseURL:          DefaultRAGServiceURL,
			Timeout:          DefaultRAGTimeout,
			MaxResponseBytes: DefaultRAGMaxResponseBytes,
		},
	}
}

// Load reads supported environment-variable overrides and parses their typed
// values. Semantic startup validation of URLs and deployment requirements is
// intentionally handled in Phase 4B.
func Load() (Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

// LoadFromLookup makes configuration parsing independent of process state.
// It is exported for unit tests and embedding hosts.
func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg := Default()

	cfg.ListenAddress = stringValue(lookup, "GATEWAY_LISTEN_ADDRESS", cfg.ListenAddress)
	cfg.PostgresDSN = stringValue(lookup, "POSTGRES_DSN", cfg.PostgresDSN)
	cfg.Arxiv.BaseURL = stringValue(lookup, "ARXIV_SERVICE_URL", cfg.Arxiv.BaseURL)
	cfg.Evaluator.BaseURL = stringValue(lookup, "EVAL_SERVICE_URL", cfg.Evaluator.BaseURL)
	cfg.RAG.BaseURL = stringValue(lookup, "RAG_SERVICE_URL", cfg.RAG.BaseURL)

	var err error
	if cfg.RequestTimeout, err = durationValue(lookup, "GATEWAY_REQUEST_TIMEOUT", cfg.RequestTimeout); err != nil {
		return Config{}, err
	}
	if cfg.StoreLogTimeout, err = durationValue(lookup, "STORE_LOG_TIMEOUT", cfg.StoreLogTimeout); err != nil {
		return Config{}, err
	}
	if cfg.Arxiv.Timeout, err = durationValue(lookup, "ARXIV_TIMEOUT", cfg.Arxiv.Timeout); err != nil {
		return Config{}, err
	}
	if cfg.Evaluator.Timeout, err = durationValue(lookup, "EVAL_TIMEOUT", cfg.Evaluator.Timeout); err != nil {
		return Config{}, err
	}
	if cfg.RAG.Timeout, err = durationValue(lookup, "RAG_TIMEOUT", cfg.RAG.Timeout); err != nil {
		return Config{}, err
	}
	if cfg.Arxiv.MaxResponseBytes, err = positiveIntValue(lookup, "ARXIV_MAX_RESPONSE_BYTES", cfg.Arxiv.MaxResponseBytes); err != nil {
		return Config{}, err
	}
	if cfg.Evaluator.MaxResponseBytes, err = positiveIntValue(lookup, "EVAL_MAX_RESPONSE_BYTES", cfg.Evaluator.MaxResponseBytes); err != nil {
		return Config{}, err
	}
	if cfg.RAG.MaxResponseBytes, err = positiveIntValue(lookup, "RAG_MAX_RESPONSE_BYTES", cfg.RAG.MaxResponseBytes); err != nil {
		return Config{}, err
	}

	// Rate limit configuration
	if cfg.RateLimit.RequestsPerMinute, err = positiveIntValue(lookup, "GATEWAY_RATE_LIMIT_RPM", cfg.RateLimit.RequestsPerMinute); err != nil {
		return Config{}, err
	}
	if cfg.RateLimit.Burst, err = positiveIntValue(lookup, "GATEWAY_RATE_LIMIT_BURST", cfg.RateLimit.Burst); err != nil {
		return Config{}, err
	}

	// Auth configuration
	apiKeysStr := stringValue(lookup, "GATEWAY_API_KEYS", "")
	if apiKeysStr != "" {
		for _, k := range strings.Split(apiKeysStr, ",") {
			key := strings.TrimSpace(k)
			if key != "" {
				cfg.Auth.APIKeys = append(cfg.Auth.APIKeys, key)
			}
		}
	}
	cfg.Auth.HeaderName = stringValue(lookup, "GATEWAY_AUTH_HEADER_NAME", cfg.Auth.HeaderName)
	cfg.Auth.HeaderPrefix = stringValue(lookup, "GATEWAY_AUTH_HEADER_PREFIX", cfg.Auth.HeaderPrefix)
	cfg.Auth.AdminKey = stringValue(lookup, "GATEWAY_ADMIN_KEY", cfg.Auth.AdminKey)

	// Key tiers: "key1:pro,key2:enterprise"
	tiersStr := stringValue(lookup, "GATEWAY_KEY_TIERS", "")
	if tiersStr != "" {
		cfg.Auth.KeyTiers = make(map[string]string)
		for _, pair := range strings.Split(tiersStr, ",") {
			parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
			if len(parts) == 2 {
				cfg.Auth.KeyTiers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	// Postgres pool configuration
	if cfg.PostgresPool.MaxOpenConns, err = positiveIntValue(lookup, "POSTGRES_POOL_MAX_OPEN", cfg.PostgresPool.MaxOpenConns); err != nil {
		return Config{}, err
	}
	if cfg.PostgresPool.MaxIdleConns, err = positiveIntValue(lookup, "POSTGRES_POOL_MAX_IDLE", cfg.PostgresPool.MaxIdleConns); err != nil {
		return Config{}, err
	}
	poolLifetimeStr := stringValue(lookup, "POSTGRES_POOL_MAX_LIFETIME", cfg.PostgresPool.ConnMaxLifetime.String())
	if d, err := time.ParseDuration(poolLifetimeStr); err == nil {
		cfg.PostgresPool.ConnMaxLifetime = d
	}
	poolIdleStr := stringValue(lookup, "POSTGRES_POOL_MAX_IDLE_TIME", cfg.PostgresPool.ConnMaxIdleTime.String())
	if d, err := time.ParseDuration(poolIdleStr); err == nil {
		cfg.PostgresPool.ConnMaxIdleTime = d
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// Validate performs semantic startup validation on configuration values.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddress) == "" {
		return fmt.Errorf("GATEWAY_LISTEN_ADDRESS cannot be empty")
	}
	if err := validateServiceConfig("ARXIV_SERVICE_URL", c.Arxiv); err != nil {
		return err
	}
	if err := validateServiceConfig("EVAL_SERVICE_URL", c.Evaluator); err != nil {
		return err
	}
	if err := validateServiceConfig("RAG_SERVICE_URL", c.RAG); err != nil {
		return err
	}
	if err := validateRateLimitConfig(c.RateLimit); err != nil {
		return err
	}
	if err := validateAuthConfig(c.Auth); err != nil {
		return err
	}
	if err := validatePostgresPoolConfig(c.PostgresPool); err != nil {
		return err
	}
	return nil
}

func validateServiceConfig(name string, s ServiceConfig) error {
	base := strings.TrimSpace(s.BaseURL)
	if base == "" {
		return fmt.Errorf("%s cannot be empty", name)
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return fmt.Errorf("%s must start with http:// or https://, got %q", name, base)
	}
	if s.Timeout <= 0 {
		return fmt.Errorf("%s timeout must be positive", name)
	}
	if s.MaxResponseBytes <= 0 {
		return fmt.Errorf("%s max response bytes must be positive", name)
	}
	return nil
}

func validateRateLimitConfig(r RateLimitConfig) error {
	if r.RequestsPerMinute <= 0 {
		return fmt.Errorf("GATEWAY_RATE_LIMIT_RPM must be positive")
	}
	if r.Burst <= 0 {
		return fmt.Errorf("GATEWAY_RATE_LIMIT_BURST must be positive")
	}
	return nil
}

func validateAuthConfig(a AuthConfig) error {
	if a.HeaderName == "" {
		return fmt.Errorf("GATEWAY_AUTH_HEADER_NAME cannot be empty")
	}
	if a.HeaderPrefix == "" {
		return fmt.Errorf("GATEWAY_AUTH_HEADER_PREFIX cannot be empty")
	}
	return nil
}

func validatePostgresPoolConfig(p PostgresPoolConfig) error {
	if p.MaxOpenConns <= 0 {
		return fmt.Errorf("POSTGRES_POOL_MAX_OPEN must be positive")
	}
	if p.MaxIdleConns <= 0 {
		return fmt.Errorf("POSTGRES_POOL_MAX_IDLE must be positive")
	}
	if p.ConnMaxLifetime <= 0 {
		return fmt.Errorf("POSTGRES_POOL_MAX_LIFETIME must be positive")
	}
	if p.ConnMaxIdleTime <= 0 {
		return fmt.Errorf("POSTGRES_POOL_MAX_IDLE_TIME must be positive")
	}
	return nil
}

func stringValue(lookup func(string) (string, bool), key, fallback string) string {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func durationValue(lookup func(string) (string, bool), key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration, got %q", key, raw)
	}
	return value, nil
}

func positiveIntValue(lookup func(string) (string, bool), key string, fallback int64) (int64, error) {
	raw, ok := lookup(key)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, raw)
	}
	return value, nil
}
