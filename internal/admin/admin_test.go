package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/middleware"
)

func TestRotateKeyHandler_Success(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "")

	body := `{"new_keys":["new-key-1","new-key-2"]}`
	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp KeyRotationResponse
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp.Status != "success" {
		t.Errorf("expected success, got %s", resp.Status)
	}
	if resp.Count != 2 {
		t.Errorf("expected 2 keys, got %d", resp.Count)
	}

	// Verify old key is gone
	if store.IsValid("old-key") {
		t.Error("old key should be invalid after rotation")
	}
	// Verify new keys are valid
	if !store.IsValid("new-key-1") {
		t.Error("new-key-1 should be valid")
	}
	if !store.IsValid("new-key-2") {
		t.Error("new-key-2 should be valid")
	}
}

func TestRotateKeyHandler_WithAdminKey(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "admin-secret")

	// Wrong admin key
	body := `{"new_keys":["new-key"],"admin_key":"wrong"}`
	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong admin key, got %d", rr.Code)
	}

	// Correct admin key
	body = `{"new_keys":["new-key"],"admin_key":"admin-secret"}`
	req = httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 with correct admin key, got %d", rr.Code)
	}
}

func TestRotateKeyHandler_EmptyKeys(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "")

	body := `{"new_keys":[]}`
	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 with empty keys, got %d", rr.Code)
	}
}

func TestRotateKeyHandler_InvalidMethod(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "")

	req := httptest.NewRequest("GET", "/admin/rotate-key", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", rr.Code)
	}
}

func TestKeyStatusHandler(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"sk-live-abc123", "sk-live-def456"})
	handler := KeyStatusHandler(store)

	req := httptest.NewRequest("GET", "/admin/key-status", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["key_count"].(float64) != 2 {
		t.Errorf("expected 2 keys, got %v", resp["key_count"])
	}
}

func TestMaskKeys(t *testing.T) {
	keys := []string{"sk-live-abc123", "short", "1234567890"}
	masked := maskKeys(keys)

	if masked[0] != "sk-l****c123" {
		t.Errorf("expected sk-l****c123, got %s", masked[0])
	}
	if masked[1] != "****" {
		t.Errorf("expected ****, got %s", masked[1])
	}
	if masked[2] != "1234****7890" {
		t.Errorf("expected 1234****7890, got %s", masked[2])
	}
}

func TestParseKeys(t *testing.T) {
	keys := ParseKeys("key1, key2 , key3")
	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}
	if keys[0] != "key1" || keys[1] != "key2" || keys[2] != "key3" {
		t.Errorf("unexpected keys: %v", keys)
	}

	// Empty string
	keys = ParseKeys("")
	if len(keys) != 0 {
		t.Errorf("expected 0 keys, got %d", len(keys))
	}
}