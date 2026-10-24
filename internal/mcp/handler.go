package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

const maxMCPBodyBytes = 1 << 20 // 1 MiB

// GetTraceID retrieves trace ID from context
func GetTraceID(ctx context.Context) string {
	if traceID, ok := ctx.Value(TraceIDKey).(string); ok {
		return traceID
	}
	return ""
}

// Handler wraps MCP server for HTTP
type Handler struct {
	server *Server
}

// NewHandler creates a new HTTP handler for MCP
func NewHandler(server *Server) *Handler {
	return &Handler{
		server: server,
	}
}

// ServeHTTP handles HTTP requests for MCP
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests. 2026-07-28 removed the GET stream endpoint.
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// DNS-rebinding protection: when a browser supplies an Origin it must be
	// the same origin as this endpoint. Non-browser clients omit the header.
	if !originAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"jsonrpc": "2.0",
			"error":   NewInvalidRequestError("origin not allowed"),
		})
		return
	}

	// Limit request body size to prevent DoS
	r.Body = http.MaxBytesReader(w, r.Body, maxMCPBodyBytes)

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, nil, NewInvalidRequestError("failed to read request body"))
		return
	}
	defer r.Body.Close()

	req, isNotification, jsonErr := parseMessage(body)
	if jsonErr != nil {
		writeJSONError(w, statusForError(jsonErr.Code), nil, jsonErr)
		return
	}

	// Notifications carry no ID and expect no response body.
	if isNotification {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// The HTTP headers mirror body fields. Reject missing or mismatched values
	// before executing anything, so intermediaries and this server agree on
	// the request being processed.
	if jsonErr := validateRequestHeaders(r, req); jsonErr != nil {
		writeJSONError(w, statusForError(jsonErr.Code), req.ID, jsonErr)
		return
	}

	// Handle MCP request (pass trace ID from X-Request-ID header if present)
	traceID := r.Header.Get("X-Request-ID")
	if traceID == "" {
		traceID = r.Header.Get("X-Trace-ID")
	}
	response, err := h.server.HandleRequest(r.Context(), req, traceID)
	if err != nil {
		log.Printf("[MCP] Error handling request: %v", err)
		writeJSONError(w, http.StatusInternalServerError, req.ID, NewInternalError("internal server error"))
		return
	}

	// Parse response to determine appropriate HTTP status code
	var jsonResp JSONRPCResponse
	status := http.StatusOK
	if err := json.Unmarshal(response, &jsonResp); err == nil && jsonResp.Error != nil {
		status = statusForError(jsonResp.Error.Code)
	}
	writeRawJSON(w, status, response)
}

// parseMessage validates the JSON-RPC envelope and distinguishes requests
// from notifications by the presence of an id member.
func parseMessage(body []byte) (*JSONRPCRequest, bool, *JSONRPCError) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, NewParseError(err.Error())
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false, NewInvalidRequestError(err.Error())
	}
	if req.JSONRPC != "2.0" {
		return nil, false, NewInvalidRequestError("jsonrpc version must be 2.0")
	}
	if req.Method == "" {
		return nil, false, NewInvalidRequestError("method is required")
	}

	idRaw, hasID := raw["id"]
	if !hasID {
		return &req, true, nil
	}

	var id interface{}
	if err := json.Unmarshal(idRaw, &id); err != nil {
		return nil, false, NewInvalidRequestError("id must be a string or number")
	}
	switch value := id.(type) {
	case string:
		req.ID = value
	case float64:
		req.ID = value
	case nil:
		return nil, false, NewInvalidRequestError("id must not be null")
	default:
		return nil, false, NewInvalidRequestError("id must be a string or number")
	}
	return &req, false, nil
}

// validateRequestHeaders enforces the 2026-07-28 Streamable HTTP request
// metadata rules and the required per-request _meta fields.
func validateRequestHeaders(r *http.Request, req *JSONRPCRequest) *JSONRPCError {
	meta := req.Meta()
	if meta == nil {
		return NewInvalidParamsError("missing _meta in params")
	}

	bodyVersion, ok := meta["io.modelcontextprotocol/protocolVersion"].(string)
	if !ok || bodyVersion == "" {
		return NewInvalidParamsError("missing io.modelcontextprotocol/protocolVersion in params._meta")
	}
	if _, ok := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]interface{}); !ok {
		return NewInvalidParamsError("missing io.modelcontextprotocol/clientCapabilities in params._meta")
	}

	headerVersion := r.Header.Get("MCP-Protocol-Version")
	if headerVersion == "" {
		return NewHeaderMismatchError("missing MCP-Protocol-Version header")
	}
	if headerVersion != bodyVersion {
		return NewHeaderMismatchError(fmt.Sprintf(
			"MCP-Protocol-Version header value %q does not match body value %q",
			headerVersion, bodyVersion,
		))
	}
	if !isSupportedProtocolVersion(headerVersion) {
		return NewUnsupportedProtocolVersionError(headerVersion)
	}

	methodHeader := r.Header.Get("Mcp-Method")
	if methodHeader == "" {
		return NewHeaderMismatchError("missing Mcp-Method header")
	}
	if methodHeader != req.Method {
		return NewHeaderMismatchError(fmt.Sprintf(
			"Mcp-Method header value %q does not match body value %q",
			methodHeader, req.Method,
		))
	}

	if req.Method == "tools/call" {
		nameHeader := r.Header.Get("Mcp-Name")
		if nameHeader == "" {
			return NewHeaderMismatchError("missing Mcp-Name header")
		}
		bodyName, _ := req.Params["name"].(string)
		if bodyName == "" {
			return NewInvalidParamsError("missing or invalid tool name")
		}
		if nameHeader != bodyName {
			return NewHeaderMismatchError(fmt.Sprintf(
				"Mcp-Name header value %q does not match body value %q",
				nameHeader, bodyName,
			))
		}
	}

	return nil
}

func isSupportedProtocolVersion(version string) bool {
	for _, supported := range SupportedProtocolVersions {
		if version == supported {
			return true
		}
	}
	return false
}

func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func statusForError(code int) int {
	switch code {
	case ParseError, InvalidRequest, InvalidParams, HeaderMismatch,
		MissingRequiredClientCapability, UnsupportedProtocolVersion:
		return http.StatusBadRequest
	case MethodNotFound:
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeJSONError(w http.ResponseWriter, status int, id interface{}, jsonErr *JSONRPCError) {
	writeJSON(w, status, JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   jsonErr,
	})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("[MCP] Error writing response: %v", err)
	}
}

func writeRawJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Printf("[MCP] Error writing response: %v", err)
	}
}

// HandleMCP is a convenience function to create an HTTP handler
func HandleMCP(server *Server) http.Handler {
	return NewHandler(server)
}
