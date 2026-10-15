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
	DefaultRAGServiceURL            = "http://arxiv-rag-service:8003"
	DefaultRAGTimeout               = 10 * time.Second
	DefaultRAGMaxResponseBytes      = 4 << 20
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
	GroqAPIKey      string
	Arxiv           ServiceConfig
	Evaluator       ServiceConfig
	RAG             ServiceConfig
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
	cfg.GroqAPIKey = stringValue(lookup, "GROQ_API_KEY", "")
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

	return cfg, nil
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
