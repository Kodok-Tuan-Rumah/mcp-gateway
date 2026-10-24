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

func serviceWithHealthStatus(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected readiness path: %s", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
}

func unavailableServiceURL(t *testing.T) string {
	t.Helper()
	server := serviceWithHealthStatus(t, http.StatusOK)
	url := server.URL
	server.Close()
	return url
}

func TestReady_AllDependenciesUp(t *testing.T) {
	arxiv := serviceWithHealthStatus(t, http.StatusOK)
	defer arxiv.Close()
	evaluator := serviceWithHealthStatus(t, http.StatusOK)
	defer evaluator.Close()
	rag := serviceWithHealthStatus(t, http.StatusOK)
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
	for _, name := range []string{"answers", "arxiv", "research_rag"} {
		if capabilities[name] != true {
			t.Errorf("expected capability %s=true, got %v", name, capabilities[name])
		}
	}
}

func TestReady_EvaluatorUnavailableDoesNotBlockAnswers(t *testing.T) {
	arxiv := serviceWithHealthStatus(t, http.StatusOK)
	defer arxiv.Close()
	evaluator := serviceWithHealthStatus(t, http.StatusServiceUnavailable)
	defer evaluator.Close()
	rag := serviceWithHealthStatus(t, http.StatusOK)
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
}

func TestReady_OneAnswerBackendDownStillServesOther(t *testing.T) {
	evaluator := serviceWithHealthStatus(t, http.StatusOK)
	defer evaluator.Close()
	rag := serviceWithHealthStatus(t, http.StatusOK)
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

func TestReady_NoAnswerBackendAvailableReturns503(t *testing.T) {
	evaluator := serviceWithHealthStatus(t, http.StatusOK)
	defer evaluator.Close()

	ts := readyRouter(t, unavailableServiceURL(t), evaluator.URL, unavailableServiceURL(t))
	defer ts.Close()

	resp := get(t, ts, "/ready")
	body := decodeJSON(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when both answer backends are down, got %d: %v", resp.StatusCode, body)
	}
	if body["status"] != "degraded" {
		t.Errorf("expected status=degraded, got %v", body["status"])
	}
	capabilities := body["capabilities"].(map[string]interface{})
	if capabilities["answers"] != false {
		t.Errorf("answers must be false, got %v", capabilities["answers"])
	}
}
