// Package middleware provides HTTP middleware for the MCP gateway.
//
// It implements rate limiting (token bucket) and API key authentication
// for public-facing endpoints.
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ──────────────────────────── Tier Configuration ────────────────────────────

// TierConfig defines rate limit parameters for a tier.
type TierConfig struct {
	Name                string
	RequestsPerMinute    int64
	Burst               int64
}

// Predefined tiers.
var (
	TierFree = TierConfig{Name: "free", RequestsPerMinute: 30, Burst: 5}
	TierPro  = TierConfig{Name: "pro", RequestsPerMinute: 120, Burst: 20}
	TierEnt  = TierConfig{Name: "enterprise", RequestsPerMinute: 600, Burst: 50}
)

// DefaultTier is the tier assigned to keys without explicit tier assignment.
var DefaultTier = TierFree

// ──────────────────────────── API Key Store ────────────────────────────

// APIKeyStore is a thread-safe store for valid API keys with per-key tiers.
type APIKeyStore struct {
	mu    sync.RWMutex
	keys  map[string]struct{}
	tiers map[string]TierConfig // key → tier (empty = default tier)
	orgs  map[string]string     // key → org_id (empty = key itself)
}

// NewAPIKeyStore creates a new key store with the given keys (all default tier).
func NewAPIKeyStore(keys []string) *APIKeyStore {
	s := &APIKeyStore{
		keys:  make(map[string]struct{}, len(keys)),
		tiers: make(map[string]TierConfig),
		orgs:  make(map[string]string),
	}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			s.orgs[k] = k
		}
	}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			s.keys[k] = struct{}{}
		}
	}
	return s
}

// NewAPIKeyStoreWithTiers creates a key store with explicit tier assignments.
// tiers is a map of key → tier name (e.g., "free", "pro", "enterprise").
func NewAPIKeyStoreWithTiers(keys []string, tierMap map[string]string) *APIKeyStore {
	s := NewAPIKeyStore(keys)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, tierName := range tierMap {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		s.tiers[key] = tierFromName(tierName)
	}
	return s
}

// tierFromName converts a tier name string to TierConfig.
func tierFromName(name string) TierConfig {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "pro":
		return TierPro
	case "enterprise", "ent":
		return TierEnt
	default:
		return TierFree
	}
}

// IsValid checks if a key is valid.
func (s *APIKeyStore) IsValid(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.keys[key]
	return ok
}

// Tier returns the tier for a given key.
func (s *APIKeyStore) Tier(key string) TierConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t, ok := s.tiers[key]; ok {
		return t
	}
	return DefaultTier
}

// OrgID returns the org id for a given key (defaults to the key itself).
func (s *APIKeyStore) OrgID(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if o, ok := s.orgs[key]; ok && o != "" {
		return o
	}
	return key
}

// SetTier sets the tier for a key.
func (s *APIKeyStore) SetTier(key, tierName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tiers[key] = tierFromName(tierName)
}

// Add adds a new key to the store (default tier).
func (s *APIKeyStore) Add(key string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key] = struct{}{}
	s.orgs[key] = key
}

// AddWithTier adds a new key with a specific tier.
func (s *APIKeyStore) AddWithTier(key, tierName string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys[key] = struct{}{}
	s.tiers[key] = tierFromName(tierName)
	s.orgs[key] = key
}

// Remove removes a key from the store.
func (s *APIKeyStore) Remove(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, key)
	delete(s.tiers, key)
	delete(s.orgs, key)
}

// Replace replaces all keys with the new set (default tier).
func (s *APIKeyStore) Replace(keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = make(map[string]struct{}, len(keys))
	s.tiers = make(map[string]TierConfig)
	s.orgs = make(map[string]string)
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			s.keys[k] = struct{}{}
			s.orgs[k] = k
		}
	}
}

// ReplaceWithTiers replaces all keys with tier assignments.
func (s *APIKeyStore) ReplaceWithTiers(keys []string, tierMap map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = make(map[string]struct{}, len(keys))
	s.tiers = make(map[string]TierConfig)
	s.orgs = make(map[string]string)
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" {
			s.keys[k] = struct{}{}
			s.orgs[k] = k
		}
	}
	for key, tierName := range tierMap {
		key = strings.TrimSpace(key)
		if key != "" {
			s.tiers[key] = tierFromName(tierName)
		}
	}
}

// List returns all valid keys.
func (s *APIKeyStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.keys))
	for k := range s.keys {
		keys = append(keys, k)
	}
	return keys
}

// Count returns the number of valid keys.
func (s *APIKeyStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.keys)
}

// ──────────────────────────── Auth Middleware ────────────────────────────

// AuthConfig holds the configuration for API key authentication.
type AuthConfig struct {
	KeyStore     *APIKeyStore
	HeaderName   string
	HeaderPrefix string
}

func newAuthConfig(keys []string, headerName, headerPrefix string) *AuthConfig {
	return &AuthConfig{
		KeyStore:     NewAPIKeyStore(keys),
		HeaderName:   headerName,
		HeaderPrefix: headerPrefix,
	}
}

// Auth returns a chi middleware function for API key authentication.
func Auth(keys []string, headerName, headerPrefix string) func(http.Handler) http.Handler {
	auth := newAuthConfig(keys, headerName, headerPrefix)
	enabled := len(keys) > 0
	exempt := map[string]bool{
		"/health":  true,
		"/ready":   true,
		"/metrics": true,
	}

	return func(next http.Handler) http.Handler {
		return &authHandler{
			next:    next,
			auth:    auth,
			enabled: enabled,
			exempt:  exempt,
		}
	}
}

// AuthWithStore returns a chi middleware function that uses an existing
// APIKeyStore. This allows runtime key updates.
func AuthWithStore(store *APIKeyStore, headerName, headerPrefix string) func(http.Handler) http.Handler {
	auth := &AuthConfig{
		KeyStore:     store,
		HeaderName:   headerName,
		HeaderPrefix: headerPrefix,
	}
	enabled := store.Count() > 0
	exempt := map[string]bool{
		"/health":  true,
		"/ready":   true,
		"/metrics": true,
	}

	return func(next http.Handler) http.Handler {
		return &authHandler{
			next:    next,
			auth:    auth,
			enabled: enabled,
			exempt:  exempt,
		}
	}
}

type authHandler struct {
	next    http.Handler
	auth    *AuthConfig
	enabled bool
	exempt  map[string]bool
}

func (h *authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	enabled := h.enabled
	if h.auth.KeyStore != nil && h.auth.KeyStore.Count() > 0 {
		enabled = true
	}

	if !enabled || h.exempt[r.URL.Path] {
		ctx := context.WithValue(r.Context(), tierContextKey, DefaultTier.Name)
		ctx = context.WithValue(ctx, orgContextKey, "")
		h.next.ServeHTTP(w, r.WithContext(ctx))
		return
	}

	key := extractAPIKey(r, h.auth.HeaderName, h.auth.HeaderPrefix)
	if key == "" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", h.auth.HeaderPrefix+" realm=\"mcp-gateway\"")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "unauthorized",
			"message": "API key required",
		})
		return
	}

	if !h.auth.KeyStore.IsValid(key) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", h.auth.HeaderPrefix+" realm=\"mcp-gateway\"")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "unauthorized",
			"message": "invalid API key",
		})
		return
	}

	orgID := ""
	if h.auth.KeyStore != nil {
		orgID = h.auth.KeyStore.OrgID(key)
	}
	ctx := context.WithValue(r.Context(), tierContextKey, h.auth.KeyStore.Tier(key).Name)
	ctx = context.WithValue(ctx, orgContextKey, orgID)
	h.next.ServeHTTP(w, r.WithContext(ctx))
}

// contextKey is an unexported type for context keys in this package.
type contextKey string

// tierContextKey carries the resolved tier name through the request context.
const tierContextKey contextKey = "api_key_tier"
const orgContextKey contextKey = "org_id"

// TierFromContext returns the tier name resolved by auth middleware, or "" if
// auth did not run (e.g. disabled or exempt path).
func TierFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(tierContextKey).(string); ok {
		return v
	}
	return ""
}

// OrgIDFromContext returns the org id resolved by auth middleware, or "".
func OrgIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(orgContextKey).(string); ok {
		return v
	}
	return ""
}

func extractAPIKey(r *http.Request, headerName, headerPrefix string) string {
	val := r.Header.Get(headerName)
	if val != "" {
		if headerPrefix != "" {
			prefix := headerPrefix + " "
			if strings.HasPrefix(val, prefix) {
				return strings.TrimSpace(val[len(prefix):])
			}
			return strings.TrimSpace(val)
		}
		return strings.TrimSpace(val)
	}

	if xak := r.Header.Get("X-API-Key"); xak != "" {
		return strings.TrimSpace(xak)
	}

	return ""
}

// ──────────────────────────── Rate Limiting (Per-Key Tier) ────────────────────────────

// tokenBucket implements a simple token bucket rate limiter.
type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	rate   float64
	burst  float64
	last   time.Time
}

func newTokenBucket(rpm, burst int64) *tokenBucket {
	return &tokenBucket{
		tokens: float64(burst),
		rate:   float64(rpm) / 60.0,
		burst:  float64(burst),
		last:   time.Now(),
	}
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now

	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}

	if b.tokens < 1.0 {
		return false
	}

	b.tokens--
	return true
}

// PerKeyRateLimiter holds one token bucket per client key, with per-key tiers.
type PerKeyRateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	store    *APIKeyStore
	defaults TierConfig
	// tierOverrides maps key → tier override
	tierOverrides map[string]TierConfig
}

// NewPerKeyRateLimiter creates a rate limiter with per-key tier support.
func NewPerKeyRateLimiter(store *APIKeyStore, defaultTier TierConfig) *PerKeyRateLimiter {
	return &PerKeyRateLimiter{
		buckets:       make(map[string]*tokenBucket),
		store:         store,
		defaults:      defaultTier,
		tierOverrides: make(map[string]TierConfig),
	}
}

// SetTier sets a custom tier for a specific key.
func (rl *PerKeyRateLimiter) SetTier(key string, tier TierConfig) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.tierOverrides[key] = tier
	// Reset bucket for this key to use new tier
	delete(rl.buckets, key)
}

// TierFor returns the tier for a given key.
// Must be called with rl.mu held.
func (rl *PerKeyRateLimiter) TierForLocked(key string) TierConfig {
	if t, ok := rl.tierOverrides[key]; ok {
		return t
	}
	// Check store tier (store has its own lock)
	if rl.store != nil && rl.store.IsValid(key) {
		return rl.store.Tier(key)
	}
	return rl.defaults
}

// TierFor returns the tier for a given key (thread-safe).
func (rl *PerKeyRateLimiter) TierFor(key string) TierConfig {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.TierForLocked(key)
}

func (rl *PerKeyRateLimiter) getBucket(key string) *tokenBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok {
		tier := rl.TierForLocked(key)
		b = newTokenBucket(tier.RequestsPerMinute, tier.Burst)
		rl.buckets[key] = b
	}
	return b
}

// PerKeyRateLimit returns a chi middleware function for per-key rate limiting.
func PerKeyRateLimit(store *APIKeyStore, defaultTier TierConfig) func(http.Handler) http.Handler {
	limiter := NewPerKeyRateLimiter(store, defaultTier)
	exempt := map[string]bool{
		"/health":  true,
		"/ready":   true,
		"/metrics": true,
	}

	return func(next http.Handler) http.Handler {
		return &perKeyRateLimitHandler{
			next:     next,
			limiter:  limiter,
			enabled:  true,
			exempt:   exempt,
		}
	}
}

type perKeyRateLimitHandler struct {
	next    http.Handler
	limiter *PerKeyRateLimiter
	enabled bool
	exempt  map[string]bool
}

func (h *perKeyRateLimitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.enabled || h.exempt[r.URL.Path] {
		h.next.ServeHTTP(w, r)
		return
	}

	key := clientKey(r)
	// For authenticated requests, use the API key as the rate limit key
	if apiKey := extractAPIKey(r, "Authorization", "Bearer"); apiKey != "" {
		key = apiKey
	}

	if !h.limiter.getBucket(key).allow() {
		tier := h.limiter.TierFor(key)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.Header().Set("X-RateLimit-Tier", tier.Name)
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "rate_limit_exceeded",
			"message": "too many requests, please try again later",
			"tier":    tier.Name,
		})
		return
	}

	h.next.ServeHTTP(w, r)
}

// RateLimit returns a chi middleware function for token-bucket rate limiting.
// Kept for backward compatibility. Uses default tier for all clients.
func RateLimit(rpm, burst int64) func(http.Handler) http.Handler {
	limiter := newRateLimiter(rpm, burst)
	exempt := map[string]bool{
		"/health":  true,
		"/ready":   true,
		"/metrics": true,
	}

	return func(next http.Handler) http.Handler {
		return &rateLimitHandler{
			next:     next,
			limiter:  limiter,
			enabled:  rpm > 0,
			exempt:   exempt,
		}
	}
}

type rateLimitHandler struct {
	next    http.Handler
	limiter *rateLimiter
	enabled bool
	exempt  map[string]bool
}

func (h *rateLimitHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.enabled || h.exempt[r.URL.Path] {
		h.next.ServeHTTP(w, r)
		return
	}

	key := clientKey(r)
	if !h.limiter.getBucket(key).allow() {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "rate_limit_exceeded",
			"message": "too many requests, please try again later",
		})
		return
	}

	h.next.ServeHTTP(w, r)
}
// clientKey extracts the client key from the request.
func clientKey(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx >= 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if r.RemoteAddr != "" {
		if idx := strings.LastIndex(r.RemoteAddr, ":"); idx >= 0 {
			return r.RemoteAddr[:idx]
		}
		return r.RemoteAddr
	}
	return "unknown"
}

// newRateLimiter creates a rate limiter (kept for backward compat).
func newRateLimiter(rpm, burst int64) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*tokenBucket),
		rate:    rpm,
		burst:   burst,
	}
}

// rateLimiter holds one token bucket per client key.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    int64
	burst   int64
}

func (rl *rateLimiter) getBucket(key string) *tokenBucket {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	b, ok := rl.buckets[key]
	if !ok {
		b = newTokenBucket(rl.rate, rl.burst)
		rl.buckets[key] = b
	}
	return b
}
