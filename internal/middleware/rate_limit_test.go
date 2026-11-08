package middleware

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTokenBucketAllow(t *testing.T) {
	b := newTokenBucket(60, 5) // 1 token/sec, burst of 5

	// Should allow up to burst size
	for i := 0; i < 5; i++ {
		if !b.allow() {
			t.Errorf("expected allow on token %d, got reject", i+1)
		}
	}

	// Should reject when bucket is empty
	if b.allow() {
		t.Error("expected reject when bucket is empty")
	}

	// Wait for tokens to regenerate
	time.Sleep(1 * time.Second)

	// Should allow again after refill
	if !b.allow() {
		t.Error("expected allow after refill")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mw := RateLimit(2, 2) // 2 rpm, burst 2
	handler := mw(next)

	// First 2 requests should be allowed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// 3rd request should be rate limited
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}

	// Different IP should not be rate limited
	req2 := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
	req2.RemoteAddr = "10.0.0.1:12345"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("different IP: expected 200, got %d", rr2.Code)
	}
}

func TestRateLimitExemptPaths(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := RateLimit(1, 1) // Very restrictive
	handler := mw(next)

	for _, path := range []string{"/health", "/ready", "/metrics"} {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("path %s: expected 200, got %d", path, rr.Code)
		}
	}
}

func TestRateLimitClientKey(t *testing.T) {
	// Trusted proxy: 127.0.0.0/8
	_, trustedNet, _ := net.ParseCIDR("127.0.0.0/8")
	trustedProxies := []*net.IPNet{trustedNet}

	// Test X-Forwarded-For (only honored from trusted proxy)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "10.0.0.5, 20.0.0.6")
	key := clientKey(req, trustedProxies)
	if key != "10.0.0.5" {
		t.Errorf("expected 10.0.0.5, got %s", key)
	}

	// Test X-Forwarded-For ignored when not from trusted proxy
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "10.0.0.5:12345"
	req2.Header.Set("X-Forwarded-For", "99.99.99.99")
	key2 := clientKey(req2, trustedProxies)
	if key2 != "10.0.0.5" {
		t.Errorf("expected 10.0.0.5 (untrusted XFF ignored), got %s", key2)
	}

	// Test RemoteAddr fallback
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.RemoteAddr = "192.168.1.1:8080"
	key3 := clientKey(req3, nil)
	if key3 != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", key3)
	}
}

func TestAuthMiddlewareDisabled(t *testing.T) {
	// No API keys = auth disabled, all requests pass
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := Auth(nil, "Authorization", "Bearer")
	handler := mw(next)

	req := httptest.NewRequest("POST", "/mcp", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 with no auth keys, got %d", rr.Code)
	}
}

func TestAuthMiddlewareEnabled(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := Auth([]string{"test-key-123"}, "Authorization", "Bearer")
	handler := mw(next)

	// No key -> 401
	req := httptest.NewRequest("POST", "/mcp", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without key, got %d", rr.Code)
	}

	// Wrong key -> 401
	req2 := httptest.NewRequest("POST", "/mcp", nil)
	req2.Header.Set("Authorization", "Bearer wrong-key")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong key, got %d", rr2.Code)
	}

	// Correct key -> 200
	req3 := httptest.NewRequest("POST", "/mcp", nil)
	req3.Header.Set("Authorization", "Bearer test-key-123")
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Errorf("expected 200 with correct key, got %d", rr3.Code)
	}
}

func TestAuthMiddlewareExemptPaths(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := Auth([]string{"test-key"}, "Authorization", "Bearer")
	handler := mw(next)

	for _, path := range []string{"/health", "/ready", "/metrics"} {
		req := httptest.NewRequest("GET", path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("path %s: expected 200, got %d", path, rr.Code)
		}
	}
}

func TestAuthMiddlewareShareLinkExempt(t *testing.T) {
	// Share links must be accessible without an API key even when auth is
	// enabled; token validation is enforced downstream by the store.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := Auth([]string{"test-key"}, "Authorization", "Bearer")
	handler := mw(next)

	// Share link without any API key -> 200 (exempt from auth)
	req := httptest.NewRequest("GET", "/r/abc123def456", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("share link without API key: expected 200, got %d", rr.Code)
	}

	// Regular path without API key -> 401 (auth still enforced)
	req2 := httptest.NewRequest("GET", "/ask", nil)
	req2.RemoteAddr = "10.0.0.1:12345"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("regular path without API key: expected 401, got %d", rr2.Code)
	}

	// Share link with valid API key -> 200
	req3 := httptest.NewRequest("GET", "/r/abc123def456", nil)
	req3.Header.Set("Authorization", "Bearer test-key")
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Errorf("share link with valid API key: expected 200, got %d", rr3.Code)
	}
}

func TestExtractAPIKey(t *testing.T) {
	// Authorization: Bearer <key>
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Authorization", "Bearer my-secret-key")
	key := extractAPIKey(req, "Authorization", "Bearer")
	if key != "my-secret-key" {
		t.Errorf("expected my-secret-key, got %s", key)
	}

	// Authorization: <key> (no prefix)
	req2 := httptest.NewRequest("POST", "/", nil)
	req2.Header.Set("Authorization", "just-a-key")
	key2 := extractAPIKey(req2, "Authorization", "Bearer")
	if key2 != "just-a-key" {
		t.Errorf("expected just-a-key, got %s", key2)
	}

	// X-API-Key fallback
	req3 := httptest.NewRequest("POST", "/", nil)
	req3.Header.Set("X-API-Key", "fallback-key")
	key3 := extractAPIKey(req3, "Authorization", "Bearer")
	if key3 != "fallback-key" {
		t.Errorf("expected fallback-key, got %s", key3)
	}

	// No key
	req4 := httptest.NewRequest("POST", "/", nil)
	key4 := extractAPIKey(req4, "Authorization", "Bearer")
	if key4 != "" {
		t.Errorf("expected empty, got %s", key4)
	}
}

func TestRateLimitConcurrent(t *testing.T) {
	mw := RateLimit(1000, 100) // High rate for concurrency test
	handler := mw(nextOKHandler)

	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
			req.RemoteAddr = "127.0.0.1:12345"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			done <- rr.Code == http.StatusOK
		}()
	}

	successCount := 0
	for i := 0; i < 100; i++ {
		if <-done {
			successCount++
		}
	}

	// All should pass with high rate limit
	if successCount != 100 {
		t.Errorf("expected 100 successes, got %d", successCount)
	}
}

func TestAPIKeyStore_OrgIDIsTenantIDNotRawKey(t *testing.T) {
	store := NewAPIKeyStore([]string{"sk-live-secret-key"})
	tenantID := store.OrgID("sk-live-secret-key")

	// Tenant ID must not be the raw API key.
	if tenantID == "sk-live-secret-key" {
		t.Error("OrgID must not return the raw API key")
	}
	// Tenant ID must be a non-empty UUID-like value.
	if tenantID == "" {
		t.Error("OrgID must return a non-empty tenant_id")
	}
	// TenantID alias returns the same.
	if store.TenantID("sk-live-secret-key") != tenantID {
		t.Error("TenantID must match OrgID")
	}
}

func TestAPIKeyStore_EachKeyGetsUniqueTenantID(t *testing.T) {
	store := NewAPIKeyStore([]string{"key-a", "key-b"})
	a := store.OrgID("key-a")
	b := store.OrgID("key-b")
	if a == b {
		t.Error("each key must be bound to a distinct tenant_id")
	}
	if a == "" || b == "" {
		t.Error("tenant_id must not be empty")
	}
}

func TestAPIKeyStore_UnknownKeyHasNoTenant(t *testing.T) {
	store := NewAPIKeyStore([]string{"key-a"})
	if store.OrgID("unknown-key") != "" {
		t.Error("unknown key must have empty tenant_id")
	}
}

func TestAPIKeyStore_SetTenant(t *testing.T) {
	store := NewAPIKeyStore([]string{"key-a"})
	store.SetTenant("key-a", "fixed-tenant-uuid")
	if store.OrgID("key-a") != "fixed-tenant-uuid" {
		t.Error("SetTenant must bind key to explicit tenant_id")
	}
	// Setting tenant on unknown key is a no-op.
	store.SetTenant("unknown", "x")
	if store.IsValid("unknown") {
		t.Error("unknown key must not become valid via SetTenant")
	}
	// Original tenant for other keys unaffected.
	store2 := NewAPIKeyStore([]string{"key-a", "key-b"})
	store2.SetTenant("key-a", "fixed-tenant-uuid")
	if store2.OrgID("key-b") == "fixed-tenant-uuid" {
		t.Error("SetTenant on one key must not affect another key's tenant")
	}
}

func TestAPIKeyStore_AddAndRemovePreserveTenantIsolation(t *testing.T) {
	store := NewAPIKeyStore([]string{"key-a"})
	store.Add("key-b")
	a := store.OrgID("key-a")
	b := store.OrgID("key-b")
	if a == b {
		t.Error("added key must get its own tenant_id distinct from existing keys")
	}
	store.Remove("key-a")
	if store.IsValid("key-a") {
		t.Error("removed key must no longer be valid")
	}
	if store.OrgID("key-a") != "" {
		t.Error("removed key must have empty tenant_id")
	}
	if !store.IsValid("key-b") {
		t.Error("other keys must remain valid after removal")
	}
}

func TestAPIKeyStore_ReplaceRotatesKeysWithoutLeakingTenants(t *testing.T) {
	store := NewAPIKeyStore([]string{"key-old"})
	store.Replace([]string{"key-new"})
	if store.IsValid("key-old") {
		t.Error("old key must be removed by Replace")
	}
	if !store.IsValid("key-new") {
		t.Error("new key must be valid after Replace")
	}
	if store.Count() != 1 {
		t.Errorf("expected 1 key after Replace, got %d", store.Count())
	}
	if store.OrgID("key-new") == "" {
		t.Error("replaced key must have a tenant_id")
	}
}

var nextOKHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
