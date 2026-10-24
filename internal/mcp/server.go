package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// TraceIDKey is the context key for trace ID
type contextKey string

const TraceIDKey contextKey = "traceID"

// Server represents an MCP server
type Server struct {
	registry   *ToolRegistry
	serverInfo ServerInfo
}

// ServerInfo contains server metadata
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// NewServer creates a new MCP server
func NewServer(serverInfo ServerInfo) *Server {
	return &Server{
		registry:   NewToolRegistry(),
		serverInfo: serverInfo,
	}
}

// ToolRegistry manages MCP tools
type ToolRegistry struct {
	tools map[string]Tool
}

// Tool represents an MCP tool
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Handler     ToolHandlerFunc
}

// ToolHandlerFunc is the function signature for tool execution
type ToolHandlerFunc func(ctx context.Context, params map[string]interface{}) (interface{}, error)

// NewToolRegistry creates a new tool registry
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// RegisterTool registers a tool in the registry
func (r *ToolRegistry) RegisterTool(tool Tool) {
	r.tools[tool.Name] = tool
}

// ListTools returns all registered tools
func (r *ToolRegistry) ListTools() []Tool {
	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// GetTool retrieves a tool by name
func (r *ToolRegistry) GetTool(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// HandleRequest processes a parsed MCP JSON-RPC request.
func (s *Server) HandleRequest(ctx context.Context, req *JSONRPCRequest, traceID string) ([]byte, error) {
	// Route to appropriate handler
	switch req.Method {
	case "server/discover":
		return s.handleDiscover(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(ctx, req, traceID)
	case "initialize", "initialized":
		// 2026-07-28 removed the handshake. Point legacy clients at the
		// supported per-request versions instead of failing opaquely.
		return marshalErrorResponse(req.ID, NewLegacyInitializeError(req.Method))
	default:
		return marshalErrorResponse(req.ID, NewMethodNotFoundError(req.Method))
	}
}

// handleDiscover reports the supported protocol versions, capabilities, and
// server identity without relying on any prior connection state.
func (s *Server) handleDiscover(req *JSONRPCRequest) ([]byte, error) {
	result := map[string]interface{}{
		"resultType":        "complete",
		"supportedVersions": SupportedProtocolVersions,
		"capabilities":      s.capabilities(),
		"_meta": map[string]interface{}{
			"io.modelcontextprotocol/serverInfo": s.serverInfo,
		},
	}
	return marshalResultResponse(req.ID, result)
}

func (s *Server) capabilities() map[string]interface{} {
	return map[string]interface{}{
		"tools": map[string]interface{}{
			"listChanged": false,
		},
	}
}

// handleToolsList handles the tools/list request
func (s *Server) handleToolsList(req *JSONRPCRequest) ([]byte, error) {
	tools := s.registry.ListTools()
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	// Convert tools to MCP format
	mcpTools := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		mcpTool := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		}
		mcpTools = append(mcpTools, mcpTool)
	}

	result := map[string]interface{}{
		"resultType": "complete",
		"tools":      mcpTools,
		"_meta": map[string]interface{}{
			"io.modelcontextprotocol/serverInfo": s.serverInfo,
		},
	}

	return marshalResultResponse(req.ID, result)
}

// handleToolsCall handles the tools/call request
func (s *Server) handleToolsCall(ctx context.Context, req *JSONRPCRequest, traceID string) ([]byte, error) {
	// Extract tool name and arguments
	toolName, ok := req.Params["name"].(string)
	if !ok {
		return marshalErrorResponse(req.ID, NewInvalidParamsError("missing or invalid tool name"))
	}

	arguments, ok := req.Params["arguments"].(map[string]interface{})
	if !ok {
		return marshalErrorResponse(req.ID, NewInvalidParamsError("missing or invalid arguments"))
	}

	// Get tool from registry. An unknown tool is an invalid params error, not
	// a missing RPC method (2026-07-28 tools contract).
	tool, exists := s.registry.GetTool(toolName)
	if !exists {
		return marshalErrorResponse(req.ID, NewInvalidParamsError(map[string]interface{}{
			"tool": toolName,
		}))
	}

	// Store trace ID in context for tool handlers
	ctx = context.WithValue(ctx, TraceIDKey, traceID)

	// Execute tool. Tool execution errors are actionable results with
	// isError=true, not JSON-RPC protocol errors.
	result, err := tool.Handler(ctx, arguments)
	if err != nil {
		return marshalResultResponse(req.ID, s.toolResult(err.Error(), true))
	}

	// Build response - serialize result as JSON for cleaner output
	resultJSON, err := json.Marshal(result)
	if err != nil {
		// Fallback to string representation if JSON serialization fails
		resultJSON = []byte(fmt.Sprintf("%v", result))
	}

	return marshalResultResponse(req.ID, s.toolResult(string(resultJSON), false))
}

func (s *Server) toolResult(text string, isError bool) map[string]interface{} {
	return map[string]interface{}{
		"resultType": "complete",
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": text,
			},
		},
		"isError": isError,
		"_meta": map[string]interface{}{
			"io.modelcontextprotocol/serverInfo": s.serverInfo,
		},
	}
}

func marshalResultResponse(id interface{}, result interface{}) ([]byte, error) {
	return json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func marshalErrorResponse(id interface{}, jsonErr *JSONRPCError) ([]byte, error) {
	return json.Marshal(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   jsonErr,
	})
}
