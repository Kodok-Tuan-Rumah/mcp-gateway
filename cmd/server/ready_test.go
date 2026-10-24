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

func serviceWithStatusAtPath(t *testing.T, wantPath string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != wantPath {
			t.Errorf("unexpected readiness path: %s, want %s", r.URL.Path, wantPath)
		}
		w.WriteHeader(status)
	}))
}

func healthyService(t *testing.T, wantPath string) *httptest.Server {
	t.Helper()
	return serviceWithStatusAtPath(t, wantPath, http.StatusOK)
}

func unavailableServiceURL(t *testing.T) string {
	t.Helper()
	server := healthyService(t, "/health")
	url := server.URL
	server.Close()
	return url
}

func TestReady_AllDependenciesUp(t *testing.T) {
	arxiv := healthyService(t, "/health")
	defer arxiv.Close()
	evaluator := healthyService(t, "/ready")
	defer evaluator.Close()
	rag := healthyService(t, "/health")
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
	capabilities, ok := body["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities must be an object, got %T", body["capabilities"])
	}
	for _, name := range []string{"answers", "evaluation", "arxiv", "research_rag"} {
		if capabilities[name] != true {
			t.Errorf("expected capability %s=true, got %v", name, capabilities[name])
		}
	}
}

func TestReady_EvaluatorDownIsDegraded(t *testing.T) {
	arxiv := healthyService(t, "/health")
	defer arxiv.Close()
	evaluator := serviceWithStatusAtPath(t, "/ready", http.StatusServiceUnavailable)
	defer evaluator.Close()
	rag := healthyService(t, "/health")
	defer rag.Close()

	ts := readyRouter(t, arxiv.URL, evaluator.URL, rag.URL)
	defer ts.Close()

	resp := get(t, ts, "/ready")
	body := decodeJSON(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 while answer backends are up, got %d: %v", resp.StatusCode, body)
	}
	if body["status"] != "degraded" {
		t.Errorf("expected status=degraded, got %v", body["status"])
	}
	services := body["services"].(map[string]interface{})
	if services["evaluator"] != "degraded" {
		t.Errorf("expected evaluator=degraded, got %v", services["evaluator"])
	}
	capabilities := body["capabilities"].(map[string]interface{})
	if capabilities["answers"] != true {
		t.Errorf("answers must remain ready when evaluation is down, got %v", capabilities["answers"])
	}
	if capabilities["evaluation"] != false {
		t.Errorf("evaluation must be false when evaluator is down, got %v", capabilities["evaluation"])
	}
}

func TestReady_OneAnswerBackendDownIsDegraded(t *testing.T) {
	evaluator := healthyService(t, "/ready")
	defer evaluator.Close()
	rag := healthyService(t, "/health")
	defer rag.Close()

	ts := readyRouter(t, unavailableServiceURL(t), evaluator.URL, rag.URL)
	defer ts.Close()

	resp := get(t, ts, "/ready")
	body := decodeJSON(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 while RAG is available, got %d: %v", resp.StatusCode, body)
	}
	if body["status"] != "degraded" {
		t.Errorf("expected status=degraded, got %v", body["status"])
	}
	services := body["services"].(map[string]interface{})
	if services["arxiv"] != "down" || services["rag"] != "up" {
		t.Errorf("unexpected service status: %v", services)
	}
	capabilities := body["capabilities"].(map[string]interface{})
	if capabilities["answers"] != true || capabilities["arxiv"] != false || capabilities["research_rag"] != true {
		t.Errorf("unexpected capability status: %v", capabilities)
	}
}

func TestReady_NoAnswerBackendIsNotReady(t *testing.T) {
	evaluator := healthyService(t, "/ready")
	defer evaluator.Close()

	ts := readyRouter(t, unavailableServiceURL(t), evaluator.URL, unavailableServiceURL(t))
	defer ts.Close()

	resp := get(t, ts, "/ready")
	body := decodeJSON(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when both answer backends are down, got %d: %v", resp.StatusCode, body)
	}
	if body["status"] != "not_ready" {
		t.Errorf("expected status=not_ready, got %v", body["status"])
	}
	capabilities := body["capabilities"].(map[string]interface{})
	if capabilities["answers"] != false {
		t.Errorf("answers must be false, got %v", capabilities["answers"])
	}
}

func TestReady_EvaluatorProbeUsesReadyPath(t *testing.T) {
	arxiv := healthyService(t, "/health")
	defer arxiv.Close()
	evaluator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer evaluator.Close()
	rag := healthyService(t, "/health")
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
	services := body["services"].(map[string]interface{})
	if services["evaluator"] != "up" {
		t.Errorf("expected evaluator=up when /ready succeeds, got %v", services["evaluator"])
	}
	capabilities := body["capabilities"].(map[string]interface{})
	if capabilities["evaluation"] != true {
		t.Errorf("expected evaluation=true, got %v", capabilities["evaluation"])
	}
}
