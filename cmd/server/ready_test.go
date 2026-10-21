package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/config"
)

// readyRouter builds the production router with downstream URLs pointing at
// test servers. Only the readiness handler is exercised here.
func readyRouter(t *testing.T, arxivURL, evaluatorURL, ragURL string) *httptest.Server {
	t.Helper()

	cfg := config.Default()
	cfg.Arxiv.BaseURL = arxivURL
	cfg.Evaluator.BaseURL = evaluatorURL
	cfg.RAG.BaseURL = ragURL

	return httptest.NewServer(newRouterWithConfig(newTestRegistry(), cfg))
}

func healthyService(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected readiness path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func TestReady_AllDependenciesUp(t *testing.T) {
	arxiv := healthyService(t)
	defer arxiv.Close()
	evaluator := healthyService(t)
	defer evaluator.Close()
	rag := healthyService(t)
	defer rag.Close()

	ts := readyRouter(t, arxiv.URL, evaluator.URL, rag.URL)
	defer ts.Close()

	resp := get(t, ts, "/ready")
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", resp.StatusCode, body)
	}
	if body["status"] != "ready" {
		t.Errorf("expected status=ready, got %v", body["status"])
	}
	services, ok := body["services"].(map[string]interface{})
	if !ok {
		t.Fatalf("services must be an object, got %T", body["services"])
	}
	for _, name := range []string{"arxiv", "evaluator", "rag"} {
		if services[name] != "up" {
			t.Errorf("expected %s=up, got %v", name, services[name])
		}
	}
}

func TestReady_DegradedAndDown(t *testing.T) {
	arxiv := healthyService(t)
	defer arxiv.Close()
	evaluator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer evaluator.Close()

	// Port 1 is closed; the probe should report down instead of blocking.
	ts := readyRouter(t, arxiv.URL, evaluator.URL, "http://127.0.0.1:1")
	defer ts.Close()

	resp := get(t, ts, "/ready")
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %v", resp.StatusCode, body)
	}
	if body["status"] != "degraded" {
		t.Errorf("expected status=degraded, got %v", body["status"])
	}
	services := body["services"].(map[string]interface{})
	if services["arxiv"] != "up" {
		t.Errorf("arxiv should still be up, got %v", services["arxiv"])
	}
	if services["evaluator"] != "degraded" {
		t.Errorf("expected evaluator=degraded, got %v", services["evaluator"])
	}
	if services["rag"] != "down" {
		t.Errorf("expected rag=down, got %v", services["rag"])
	}
}
