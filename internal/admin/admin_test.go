package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/malikimayzar/mcp-gateway/internal/middleware"
)

func TestRotateKeyHandler_AdminKeyNotConfigured(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "")

	body := `{"add_keys":["new-key"],"admin_key":"anything"}`
	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when admin key not configured, got %d", rr.Code)
	}
	// Old key must remain valid (nothing rotated).
	if !store.IsValid("old-key") {
		t.Error("old key should remain valid when admin key is not configured")
	}
}

func TestRotateKeyHandler_Success_AddKeys(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "admin-secret")

	body := `{"add_keys":["new-key-1","new-key-2"],"admin_key":"admin-secret"}`
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
	if resp.Count != 3 {
		t.Errorf("expected 3 keys, got %d", resp.Count)
	}

	// Old key must still exist (add-only rotation does not remove).
	if !store.IsValid("old-key") {
		t.Error("old key should remain valid after add-only rotation")
	}
	if !store.IsValid("new-key-1") {
		t.Error("new-key-1 should be valid")
	}
	if !store.IsValid("new-key-2") {
		t.Error("new-key-2 should be valid")
	}
}

func TestRotateKeyHandler_Atomic_AddAndRemove(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"key-a", "key-b", "key-c"})
	handler := RotateKeyHandler(store, "admin-secret")

	// Rotate: add key-d, remove key-b. key-a and key-c must survive.
	body := `{"add_keys":["key-d"],"remove_keys":["key-b"],"admin_key":"admin-secret"}`
	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	if !store.IsValid("key-a") {
		t.Error("key-a should remain valid (not in remove_keys)")
	}
	if !store.IsValid("key-c") {
		t.Error("key-c should remain valid (not in remove_keys)")
	}
	if store.IsValid("key-b") {
		t.Error("key-b should be revoked (explicitly in remove_keys)")
	}
	if !store.IsValid("key-d") {
		t.Error("key-d should be valid (in add_keys)")
	}
	if respCount(store) != 3 {
		t.Errorf("expected 3 keys after rotation, got %d", respCount(store))
	}
}

func TestRotateKeyHandler_WrongAdminKey(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "admin-secret")

	body := `{"add_keys":["new-key"],"admin_key":"wrong"}`
	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong admin key, got %d", rr.Code)
	}
	// Store must be untouched.
	if !store.IsValid("old-key") {
		t.Error("store must be untouched on wrong admin key")
	}
	if store.IsValid("new-key") {
		t.Error("new key must not be added on wrong admin key")
	}
}

func TestRotateKeyHandler_MissingAdminKey(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "admin-secret")

	body := `{"add_keys":["new-key"]}`
	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with missing admin key, got %d", rr.Code)
	}
}

func TestRotateKeyHandler_NothingToDo(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "admin-secret")

	body := `{"add_keys":[],"remove_keys":[],"admin_key":"admin-secret"}`
	req := httptest.NewRequest("POST", "/admin/rotate-key", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 with nothing to do, got %d", rr.Code)
	}
}

func TestRotateKeyHandler_InvalidMethod(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"old-key"})
	handler := RotateKeyHandler(store, "admin-secret")

	req := httptest.NewRequest("GET", "/admin/rotate-key", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", rr.Code)
	}
}

func TestKeyStatusHandler_RequiresAdminKey(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"sk-live-abc123"})
	handler := KeyStatusHandler(store, "admin-secret")

	// No admin key → 401
	req := httptest.NewRequest("GET", "/admin/key-status?admin_key=", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without admin key, got %d", rr.Code)
	}

	// Wrong admin key → 401
	req = httptest.NewRequest("GET", "/admin/key-status?admin_key=wrong", nil)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong admin key, got %d", rr.Code)
	}

	// Correct admin key → 200
	req = httptest.NewRequest("GET", "/admin/key-status?admin_key=admin-secret", nil)
	rr = httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 with correct admin key, got %d", rr.Code)
	}
}

func TestKeyStatusHandler_AdminKeyNotConfigured(t *testing.T) {
	store := middleware.NewAPIKeyStore([]string{"sk-live-abc123"})
	handler := KeyStatusHandler(store, "")

	req := httptest.NewRequest("GET", "/admin/key-status?admin_key=anything", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when admin key not configured, got %d", rr.Code)
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

func respCount(store *middleware.APIKeyStore) int {
	return store.Count()
}
