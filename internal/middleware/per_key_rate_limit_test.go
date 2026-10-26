package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPerKeyRateLimit_TierFree(t *testing.T) {
	store := NewAPIKeyStore([]string{"free-key"})
	handler := PerKeyRateLimit(store, TierFree)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Free tier: 30 RPM, burst 5
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer free-key")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// 6th request should be rate limited
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer free-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestPerKeyRateLimit_TierPro(t *testing.T) {
	store := NewAPIKeyStoreWithTiers([]string{"pro-key"}, map[string]string{"pro-key": "pro"})
	handler := PerKeyRateLimit(store, TierFree)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Pro tier: 120 RPM, burst 20
	for i := 0; i < 20; i++ {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer pro-key")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}

	// 21st request should be rate limited
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Authorization", "Bearer pro-key")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestPerKeyRateLimit_TierEnterprise(t *testing.T) {
	store := NewAPIKeyStoreWithTiers([]string{"ent-key"}, map[string]string{"ent-key": "enterprise"})
	handler := PerKeyRateLimit(store, TierFree)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Enterprise tier: 600 RPM, burst 50
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader("{}"))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Authorization", "Bearer ent-key")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, rr.Code)
		}
	}
}

func TestPerKeyRateLimit_SetTier(t *testing.T) {
	store := NewAPIKeyStore([]string{"test-key"})
	limiter := NewPerKeyRateLimiter(store, TierFree)

	// Initially free tier
	tier := limiter.TierFor("test-key")
	if tier.Name != "free" {
		t.Errorf("expected free, got %s", tier.Name)
	}

	// Upgrade to pro
	limiter.SetTier("test-key", TierPro)
	tier = limiter.TierFor("test-key")
	if tier.Name != "pro" {
		t.Errorf("expected pro after upgrade, got %s", tier.Name)
	}
}

func TestPerKeyRateLimit_TierForUnknownKey(t *testing.T) {
	store := NewAPIKeyStore([]string{})
	limiter := NewPerKeyRateLimiter(store, TierPro)

	// Unknown key gets default tier
	tier := limiter.TierFor("unknown-key")
	if tier.Name != "pro" {
		t.Errorf("expected default tier (pro), got %s", tier.Name)
	}
}

func TestPerKeyRateLimit_ExemptPaths(t *testing.T) {
	store := NewAPIKeyStore([]string{"test-key"})
	handler := PerKeyRateLimit(store, TierFree)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

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

func TestTierFromName(t *testing.T) {
	cases := []struct {
		name     string
		expected string
	}{
		{"free", "free"},
		{"pro", "pro"},
		{"enterprise", "enterprise"},
		{"ent", "enterprise"},
		{"unknown", "free"},
		{"", "free"},
		{"PRO", "pro"},
	}

	for _, tc := range cases {
		tier := tierFromName(tc.name)
		if tier.Name != tc.expected {
			t.Errorf("tierFromName(%q): expected %s, got %s", tc.name, tc.expected, tier.Name)
		}
	}
}