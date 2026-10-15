package registry

import (
	"context"
	"fmt"
	"sync"
)

// ToolRequest adalah permintaan pemanggilan tool pada endpoint /tool.
//
// Key JSON sengaja snake_case dan harus tetap begitu: bentuk inilah yang
// didokumentasikan di README dan dipakai scripts/benchmark.py. Go tidak bisa
// menjembatani tool_name ke ToolName tanpa tag — case-insensitive matching
// tidak berlaku pada underscore — jadi menghapus tag akan membuat /tool
// menolak setiap payload yang ada di dokumentasi.
//
// Dikunci oleh contract_test.go.
type ToolRequest struct {
	ToolName string                 `json:"tool_name"`
	TraceID  string                 `json:"trace_id"`
	Params   map[string]interface{} `json:"params"`
}

// ToolResponse adalah hasil pemanggilan tool pada endpoint /tool.
//
// Bentuk ini berbeda dari GatewayResponse di internal/api/contract.go dengan
// sengaja: /tool adalah passthrough mentah ke registry dan berada di luar
// kontrak jawaban (docs/api-contract.md §2).
type ToolResponse struct {
	ToolName    string                 `json:"tool_name"`
	TraceID     string                 `json:"trace_id"`
	Success     bool                   `json:"success"`
	Data        map[string]interface{} `json:"data,omitempty"`
	Error       string                 `json:"error,omitempty"`
	TypedResult interface{}            `json:"-"`
}

type HandlerFunc func(ctx context.Context, req ToolRequest) ToolResponse

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
}

func New() *Registry {
	return &Registry{
		handlers: make(map[string]HandlerFunc),
	}
}

func (r *Registry) Register(name string, handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = handler
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	return names
}

func (r *Registry) Execute(ctx context.Context, req ToolRequest) ToolResponse {
	r.mu.RLock()
	handler, ok := r.handlers[req.ToolName]
	r.mu.RUnlock()

	if !ok {
		return ToolResponse{
			ToolName: req.ToolName,
			TraceID:  req.TraceID,
			Success:  false,
			Error:    fmt.Sprintf("tool %q not found in registry", req.ToolName),
		}
	}

	return handler(ctx, req)
}
