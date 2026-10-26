// Package admin provides administrative endpoints for the MCP gateway.
//
// Includes API key rotation without service restart.
package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/middleware"
)

// Global key store instance
var globalKeyStore *middleware.APIKeyStore

// SetGlobalKeyStore sets the global API key store.
func SetGlobalKeyStore(store *middleware.APIKeyStore) {
	globalKeyStore = store
}

// GlobalKeyStore returns the global API key store.
func GlobalKeyStore() *middleware.APIKeyStore {
	return globalKeyStore
}

// KeyRotationRequest is the request body for key rotation.
type KeyRotationRequest struct {
	NewKeys  []string `json:"new_keys"`
	AdminKey string   `json:"admin_key"`
}

// KeyRotationResponse is the response for key rotation.
type KeyRotationResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// RotateKeyHandler handles API key rotation requests.
func RotateKeyHandler(store *middleware.APIKeyStore, adminKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
			return
		}

		var req KeyRotationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}

		if adminKey != "" && req.AdminKey != adminKey {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid admin key"})
			return
		}

		if len(req.NewKeys) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "new_keys cannot be empty"})
			return
		}

		store.Replace(req.NewKeys)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(KeyRotationResponse{
			Status:  "success",
			Message: "API keys rotated successfully",
			Count:   store.Count(),
		})
	}
}

// KeyStatusHandler returns the current key count.
func KeyStatusHandler(store *middleware.APIKeyStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"key_count": store.Count(),
			"keys":      maskKeys(store.List()),
		})
	}
}

func maskKeys(keys []string) []string {
	masked := make([]string, len(keys))
	for i, k := range keys {
		if len(k) > 8 {
			masked[i] = k[:4] + "****" + k[len(k)-4:]
		} else {
			masked[i] = "****"
		}
	}
	return masked
}

func ParseKeys(s string) []string {
	var keys []string
	for _, k := range strings.Split(s, ",") {
		k = strings.TrimSpace(k)
		if k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}