package middleware

import (
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
	// Test X-Forwarded-For
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.5, 20.0.0.6")
	key := clientKey(req)
	if key != "10.0.0.5" {
		t.Errorf("expected 10.0.0.5, got %s", key)
	}

	// Test RemoteAddr fallback
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "192.168.1.1:8080"
	key2 := clientKey(req2)
	if key2 != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", key2)
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

var nextOKHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})
