// Package admin provides administrative endpoints for the MCP gateway.
//
// Includes API key rotation without service restart.
//
// All admin endpoints are fail-closed: if GATEWAY_ADMIN_KEY is not configured,
// every admin request is rejected (503) instead of being allowed open.
package admin

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/malikimayzar/mcp-gateway/internal/logging"
	"github.com/malikimayzar/mcp-gateway/internal/middleware"
)

var (
	globalKeyStore *middleware.APIKeyStore
	logger         = logging.NewLogger("mcp-gateway-admin", logging.INFO)
)

// SetGlobalKeyStore sets the global API key store.
func SetGlobalKeyStore(store *middleware.APIKeyStore) {
	globalKeyStore = store
}

// GlobalKeyStore returns the global API key store.
func GlobalKeyStore() *middleware.APIKeyStore {
	return globalKeyStore
}

// KeyRotationRequest is the request body for key rotation.
//
// Keys listed in add_keys are added; keys listed in remove_keys are revoked.
// Keys that appear in neither list are left untouched, so rotation never
// removes a key without an explicit request.
type KeyRotationRequest struct {
	AddKeys    []string `json:"add_keys"`
	RemoveKeys []string `json:"remove_keys"`
	AdminKey   string   `json:"admin_key"`
}

// KeyRotationResponse is the response for key rotation.
type KeyRotationResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// RotateKeyHandler handles API key rotation requests.
//
// Fail-closed behavior:
//   - adminKey == "" (not configured) → 503 Service Unavailable
//   - wrong admin key → 401 Unauthorized (constant-time compare)
func RotateKeyHandler(store *middleware.APIKeyStore, adminKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
			return
		}

		// Admin key must be configured. An empty admin key means admin
		// operations are disabled; fail closed instead of allowing open access.
		if adminKey == "" {
			logger.Warning("admin key rotation attempted but admin key not configured", logging.Fields{
				"action": "rotate-key",
				"result": "admin_key_not_configured",
			})
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "admin key not configured"})
			return
		}

		var req KeyRotationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}

		// Constant-time comparison to prevent timing attacks.
		if subtle.ConstantTimeCompare([]byte(req.AdminKey), []byte(adminKey)) != 1 {
			logger.Warning("invalid admin key used for key rotation", logging.Fields{
				"action": "rotate-key",
				"result": "invalid_admin_key",
			})
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid admin key"})
			return
		}

		if len(req.AddKeys) == 0 && len(req.RemoveKeys) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "add_keys or remove_keys must be provided"})
			return
		}

		// Atomic rotation: add new keys, remove revoked keys. Neither operation
		// removes keys that were not explicitly listed in remove_keys.
		store.AddKeys(req.AddKeys)
		removed := 0
		for _, k := range req.RemoveKeys {
			if store.IsValid(k) {
				store.Remove(k)
				removed++
			}
		}

		logger.Info("API keys rotated", logging.Fields{
			"action":        "rotate-key",
			"result":        "success",
			"added_keys":    len(req.AddKeys),
			"removed_keys":  removed,
			"total_keys":    store.Count(),
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(KeyRotationResponse{
			Status:  "success",
			Message: "API keys rotated successfully",
			Count:   store.Count(),
		})
	}
}

// KeyStatusHandler returns the current key count and masked keys.
//
// Requires a valid admin key; exposes key metadata so it is protected like
// rotation.
func KeyStatusHandler(store *middleware.APIKeyStore, adminKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if adminKey == "" {
			logger.Warning("admin key status accessed but admin key not configured", logging.Fields{
				"action": "key-status",
				"result": "admin_key_not_configured",
			})
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "admin key not configured"})
			return
		}

		if r.URL.Query().Get("admin_key") != "" {
			if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("admin_key")), []byte(adminKey)) != 1 {
				logger.Warning("invalid admin key used for key status", logging.Fields{
					"action": "key-status",
					"result": "invalid_admin_key",
				})
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid admin key"})
				return
			}
		} else {
			logger.Warning("admin key status accessed without admin key", logging.Fields{
				"action": "key-status",
				"result": "missing_admin_key",
			})
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "admin key required"})
			return
		}

		logger.Info("admin key status accessed", logging.Fields{
			"action":     "key-status",
			"result":     "success",
			"key_count":  store.Count(),
		})

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
