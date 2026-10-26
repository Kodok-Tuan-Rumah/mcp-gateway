package stdio

import (
	"encoding/json"

	"github.com/malikimayzar/mcp-gateway/internal/mcp"
)

// parseMessage validates the JSON-RPC envelope for stdio transport.
// Unlike HTTP transport, stdio doesn't require header validation.
func parseMessage(body []byte) (*mcp.JSONRPCRequest, bool, *mcp.JSONRPCError) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, mcp.NewParseError(err.Error())
	}

	var req mcp.JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false, mcp.NewInvalidRequestError(err.Error())
	}
	if req.JSONRPC != "2.0" {
		return nil, false, mcp.NewInvalidRequestError("jsonrpc version must be 2.0")
	}
	if req.Method == "" {
		return nil, false, mcp.NewInvalidRequestError("method is required")
	}

	idRaw, hasID := raw["id"]
	if !hasID {
		return &req, true, nil
	}

	var id interface{}
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return nil, false, mcp.NewInvalidRequestError("id must be a string or number")
	}
	switch value := id.(type) {
	case string:
		req.ID = value
	case float64:
		req.ID = value
	case nil:
		return nil, false, mcp.NewInvalidRequestError("id must not be null")
	default:
		return nil, false, mcp.NewInvalidRequestError("id must be a string or number")
	}
	return &req, false, nil
}
