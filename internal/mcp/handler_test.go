package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testProtocolVersion = "2026-07-28"

func testServer() *Server {
	s := NewServer(ServerInfo{Name: "mcp-gateway", Version: "test"})
	s.registry.RegisterTool(Tool{
		Name:        "echo",
		Description: "Echo the call arguments",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(_ context.Context, params map[string]interface{}) (interface{}, error) {
			return params, nil
		},
	})
	return s
}

func requestMeta(version string) map[string]interface{} {
	return map[string]interface{}{
		"io.modelcontextprotocol/protocolVersion":    version,
		"io.modelcontextprotocol/clientCapabilities": map[string]interface{}{},
	}
}

func marshalRequest(t *testing.T, id interface{}, method string, params map[string]interface{}) []byte {
	t.Helper()
	if params == nil {
		params = map[string]interface{}{}
	}
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if id != nil {
		msg["id"] = id
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return body
}

func requestHeaders(method, name string) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("MCP-Protocol-Version", testProtocolVersion)
	h.Set("Mcp-Method", method)
	if name != "" {
		h.Set("Mcp-Name", name)
	}
	return h
}

func doMCP(t *testing.T, handler http.Handler, body []byte, headers http.Header, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://gateway.test/mcp", bytes.NewReader(body))
	req.Host = "gateway.test"
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) JSONRPCResponse {
	t.Helper()
	var resp JSONRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return resp
}

func discoverBody(t *testing.T) []byte {
	t.Helper()
	return marshalRequest(t, "discover-1", "server/discover", map[string]interface{}{
		"_meta": requestMeta(testProtocolVersion),
	})
}

func TestServerDiscover(t *testing.T) {
	rec := doMCP(t, NewHandler(testServer()), discoverBody(t), requestHeaders("server/discover", ""), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	resp := decodeResponse(t, rec)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is %T, want object", resp.Result)
	}
	if result["resultType"] != "complete" {
		t.Errorf("resultType = %v, want complete", result["resultType"])
	}
	versions, ok := result["supportedVersions"].([]interface{})
	if !ok || len(versions) == 0 {
		t.Fatalf("supportedVersions = %v, want non-empty list", result["supportedVersions"])
	}
	if versions[0] != testProtocolVersion {
		t.Errorf("supportedVersions[0] = %v, want %s", versions[0], testProtocolVersion)
	}
	capabilities, ok := result["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("capabilities is %T, want object", result["capabilities"])
	}
	if _, ok := capabilities["tools"]; !ok {
		t.Errorf("capabilities.tools missing: %v", capabilities)
	}
	meta, ok := result["_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("result _meta is %T, want object", result["_meta"])
	}
	serverInfo, ok := meta["io.modelcontextprotocol/serverInfo"].(map[string]interface{})
	if !ok || serverInfo["name"] != "mcp-gateway" {
		t.Errorf("serverInfo = %v, want mcp-gateway", meta["io.modelcontextprotocol/serverInfo"])
	}
}

func TestRequiredHeaders(t *testing.T) {
	body := marshalRequest(t, 1, "tools/call", map[string]interface{}{
		"name":      "echo",
		"arguments": map[string]interface{}{"query": "hello"},
		"_meta":     requestMeta(testProtocolVersion),
	})

	cases := []struct {
		name   string
		mutate func(http.Header)
	}{
		{"missing protocol version", func(h http.Header) { h.Del("MCP-Protocol-Version") }},
		{"missing method", func(h http.Header) { h.Del("Mcp-Method") }},
		{"missing tool name", func(h http.Header) { h.Del("Mcp-Name") }},
		{"protocol version mismatch", func(h http.Header) { h.Set("MCP-Protocol-Version", "2025-11-25") }},
		{"method mismatch", func(h http.Header) { h.Set("Mcp-Method", "tools/list") }},
		{"tool name mismatch", func(h http.Header) { h.Set("Mcp-Name", "other-tool") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := requestHeaders("tools/call", "echo")
			tc.mutate(headers)
			rec := doMCP(t, NewHandler(testServer()), body, headers, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			resp := decodeResponse(t, rec)
			if resp.Error == nil || resp.Error.Code != HeaderMismatch {
				t.Fatalf("error = %+v, want code %d", resp.Error, HeaderMismatch)
			}
		})
	}
}

func TestUnsupportedProtocolVersion(t *testing.T) {
	body := marshalRequest(t, 1, "tools/list", map[string]interface{}{
		"_meta": requestMeta("1900-01-01"),
	})
	headers := requestHeaders("tools/list", "")
	headers.Set("MCP-Protocol-Version", "1900-01-01")

	rec := doMCP(t, NewHandler(testServer()), body, headers, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Error == nil || resp.Error.Code != UnsupportedProtocolVersion {
		t.Fatalf("error = %+v, want code %d", resp.Error, UnsupportedProtocolVersion)
	}
	data, ok := resp.Error.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("error data is %T, want object", resp.Error.Data)
	}
	if data["requested"] != "1900-01-01" {
		t.Errorf("requested = %v, want 1900-01-01", data["requested"])
	}
	supported, ok := data["supported"].([]interface{})
	if !ok || len(supported) == 0 || supported[0] != testProtocolVersion {
		t.Errorf("supported = %v, want [%s]", data["supported"], testProtocolVersion)
	}
}

func TestRequestMetaIsRequiredInParams(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]interface{}
	}{
		{
			name:   "missing _meta",
			params: map[string]interface{}{},
		},
		{
			name: "missing protocol version",
			params: map[string]interface{}{
				"_meta": map[string]interface{}{
					"io.modelcontextprotocol/clientCapabilities": map[string]interface{}{},
				},
			},
		},
		{
			name: "missing client capabilities",
			params: map[string]interface{}{
				"_meta": map[string]interface{}{
					"io.modelcontextprotocol/protocolVersion": testProtocolVersion,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := marshalRequest(t, 1, "tools/list", tc.params)
			rec := doMCP(t, NewHandler(testServer()), body, requestHeaders("tools/list", ""), "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			resp := decodeResponse(t, rec)
			if resp.Error == nil || resp.Error.Code != InvalidParams {
				t.Fatalf("error = %+v, want code %d", resp.Error, InvalidParams)
			}
		})
	}
}

func TestTopLevelMetaIsNotAccepted(t *testing.T) {
	msg := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
		"_meta":   requestMeta(testProtocolVersion),
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := doMCP(t, NewHandler(testServer()), body, requestHeaders("tools/list", ""), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Error == nil || resp.Error.Code != InvalidParams {
		t.Fatalf("error = %+v, want code %d", resp.Error, InvalidParams)
	}
}

func TestNotificationReturns202WithoutBody(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	rec := doMCP(t, NewHandler(testServer()), body, http.Header{}, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestOriginValidation(t *testing.T) {
	cases := []struct {
		name       string
		origin     string
		wantStatus int
	}{
		{"no origin", "", http.StatusOK},
		{"same origin", "http://gateway.test", http.StatusOK},
		{"foreign origin", "https://evil.example", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doMCP(
				t,
				NewHandler(testServer()),
				discoverBody(t),
				requestHeaders("server/discover", ""),
				tc.origin,
			)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestLegacyInitializeIsNotSupported(t *testing.T) {
	body := marshalRequest(t, 1, "initialize", map[string]interface{}{
		"_meta": requestMeta(testProtocolVersion),
	})
	rec := doMCP(t, NewHandler(testServer()), body, requestHeaders("initialize", ""), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Error == nil || resp.Error.Code != MethodNotFound {
		t.Fatalf("error = %+v, want code %d", resp.Error, MethodNotFound)
	}
	data, ok := resp.Error.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("error data is %T, want object", resp.Error.Data)
	}
	supported, ok := data["supported"].([]interface{})
	if !ok || len(supported) == 0 || supported[0] != testProtocolVersion {
		t.Errorf("supported = %v, want [%s] for legacy initialize", data["supported"], testProtocolVersion)
	}
}

func TestToolsCallSuccess(t *testing.T) {
	body := marshalRequest(t, 3, "tools/call", map[string]interface{}{
		"name":      "echo",
		"arguments": map[string]interface{}{"query": "attention mechanism"},
		"_meta":     requestMeta(testProtocolVersion),
	})
	rec := doMCP(t, NewHandler(testServer()), body, requestHeaders("tools/call", "echo"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is %T, want object", resp.Result)
	}
	if result["resultType"] != "complete" {
		t.Errorf("resultType = %v, want complete", result["resultType"])
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("content = %v, want one item", result["content"])
	}
	item, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("content[0] is %T, want object", content[0])
	}
	if text, _ := item["text"].(string); !strings.Contains(text, "attention mechanism") {
		t.Errorf("tool result text = %q, want echoed arguments", text)
	}
}

func TestUnknownToolIsInvalidParams(t *testing.T) {
	body := marshalRequest(t, 4, "tools/call", map[string]interface{}{
		"name":      "missing",
		"arguments": map[string]interface{}{},
		"_meta":     requestMeta(testProtocolVersion),
	})
	rec := doMCP(t, NewHandler(testServer()), body, requestHeaders("tools/call", "missing"), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Error == nil || resp.Error.Code != InvalidParams {
		t.Fatalf("error = %+v, want code %d", resp.Error, InvalidParams)
	}
}

func TestToolExecutionErrorIsAResult(t *testing.T) {
	server := testServer()
	server.registry.RegisterTool(Tool{
		Name:        "fail",
		Description: "Always fails",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(_ context.Context, _ map[string]interface{}) (interface{}, error) {
			return nil, errors.New("invalid departure date")
		},
	})
	body := marshalRequest(t, 5, "tools/call", map[string]interface{}{
		"name":      "fail",
		"arguments": map[string]interface{}{},
		"_meta":     requestMeta(testProtocolVersion),
	})
	rec := doMCP(t, NewHandler(server), body, requestHeaders("tools/call", "fail"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Error != nil {
		t.Fatalf("tool execution error leaked into JSON-RPC error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("result is %T, want object", resp.Result)
	}
	if result["isError"] != true {
		t.Errorf("isError = %v, want true", result["isError"])
	}
	content, ok := result["content"].([]interface{})
	if !ok || len(content) != 1 {
		t.Fatalf("content = %v, want one item", result["content"])
	}
	item, _ := content[0].(map[string]interface{})
	if text, _ := item["text"].(string); !strings.Contains(text, "invalid departure date") {
		t.Errorf("error text = %q, want handler error", text)
	}
}

func TestParseErrorReturns400(t *testing.T) {
	rec := doMCP(t, NewHandler(testServer()), []byte("{not json"), http.Header{}, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeResponse(t, rec)
	if resp.Error == nil || resp.Error.Code != ParseError {
		t.Fatalf("error = %+v, want code %d", resp.Error, ParseError)
	}
}

func TestNonPostReturns405(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://gateway.test/mcp", nil)
	rec := httptest.NewRecorder()
	NewHandler(testServer()).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
