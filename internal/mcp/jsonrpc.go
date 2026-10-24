package mcp

// JSON-RPC 2.0 message types for MCP protocol
// Based on: https://modelcontextprotocol.io/specification/2026-07-28/basic.md

// ProtocolVersion is the only MCP revision this server implements.
const ProtocolVersion = "2026-07-28"

// SupportedProtocolVersions is advertised by server/discover and returned in
// UnsupportedProtocolVersionError so clients can retry with a known version.
var SupportedProtocolVersions = []string{ProtocolVersion}

// JSONRPCRequest represents a JSON-RPC 2.0 request. Protocol metadata lives in
// params._meta, as required by the 2026-07-28 per-request model.
type JSONRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id"` // string or number, MUST NOT be null
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

// Meta returns the request's params._meta object, or nil when absent.
func (r *JSONRPCRequest) Meta() map[string]interface{} {
	if r == nil || r.Params == nil {
		return nil
	}
	meta, _ := r.Params["_meta"].(map[string]interface{})
	return meta
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id"`
	Result  interface{}            `json:"result,omitempty"`
	Error   *JSONRPCError          `json:"error,omitempty"`
	Meta    map[string]interface{} `json:"_meta,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// JSONRPCNotification represents a JSON-RPC 2.0 notification (no response expected)
type JSONRPCNotification struct {
	JSONRPC string                 `json:"jsonrpc"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Meta    map[string]interface{} `json:"_meta,omitempty"`
}

// JSON-RPC 2.0 Standard Error Codes
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// MCP-specific Error Codes (from -32020 to -32099)
const (
	HeaderMismatch                  = -32020
	MissingRequiredClientCapability = -32021
	UnsupportedProtocolVersion      = -32022
)

// Helper functions for creating error responses

func NewParseError(data interface{}) *JSONRPCError {
	return &JSONRPCError{
		Code:    ParseError,
		Message: "Parse error",
		Data:    data,
	}
}

func NewInvalidRequestError(data interface{}) *JSONRPCError {
	return &JSONRPCError{
		Code:    InvalidRequest,
		Message: "Invalid Request",
		Data:    data,
	}
}

func NewMethodNotFoundError(method string) *JSONRPCError {
	return &JSONRPCError{
		Code:    MethodNotFound,
		Message: "Method not found",
		Data:    map[string]interface{}{"method": method},
	}
}

// NewLegacyInitializeError rejects the pre-2026-07-28 handshake and tells the
// client which per-request protocol versions it can retry with.
func NewLegacyInitializeError(method string) *JSONRPCError {
	return &JSONRPCError{
		Code:    MethodNotFound,
		Message: "Method not found",
		Data: map[string]interface{}{
			"method":    method,
			"supported": SupportedProtocolVersions,
		},
	}
}

func NewInvalidParamsError(data interface{}) *JSONRPCError {
	return &JSONRPCError{
		Code:    InvalidParams,
		Message: "Invalid params",
		Data:    data,
	}
}

func NewInternalError(data interface{}) *JSONRPCError {
	return &JSONRPCError{
		Code:    InternalError,
		Message: "Internal error",
		Data:    data,
	}
}

func NewMissingRequiredClientCapabilityError(capabilities []string) *JSONRPCError {
	return &JSONRPCError{
		Code:    MissingRequiredClientCapability,
		Message: "Missing required client capability",
		Data:    map[string]interface{}{"requiredCapabilities": capabilities},
	}
}

func NewUnsupportedProtocolVersionError(version string) *JSONRPCError {
	return &JSONRPCError{
		Code:    UnsupportedProtocolVersion,
		Message: "Unsupported protocol version",
		Data: map[string]interface{}{
			"supported": SupportedProtocolVersions,
			"requested": version,
		},
	}
}

func NewHeaderMismatchError(detail string) *JSONRPCError {
	return &JSONRPCError{
		Code:    HeaderMismatch,
		Message: "Header mismatch: " + detail,
	}
}
