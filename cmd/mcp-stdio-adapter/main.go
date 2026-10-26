// Package main implements an stdio adapter for MCP
//
// This adapter reads JSON-RPC 2.0 messages from stdin and forwards them
// to the HTTP MCP endpoint, then writes responses to stdout.
// This enables Claude Desktop compatibility with the HTTP-based MCP server.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultURL = "http://localhost:8090/mcp"
)

func main() {
	// Parse command-line flags
	url := flag.String("url", defaultURL, "HTTP MCP endpoint URL")
	flag.Parse()

	// Allow environment variable override
	if envURL := os.Getenv("MCP_URL"); envURL != "" {
		url = &envURL
	}

	log.Printf("[stdio-adapter] Starting MCP stdio adapter")
	log.Printf("[stdio-adapter] Target URL: %s", *url)

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	// Read from stdin line by line
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Parse JSON-RPC request
		var req map[string]interface{}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			log.Printf("[stdio-adapter] Error parsing JSON: %v", err)
			continue
		}

		// Extract method and headers
		method, _ := req["method"].(string)
		if method == "" {
			log.Printf("[stdio-adapter] Missing method in request")
			continue
		}

		// Forward to HTTP endpoint
		resp, err := forwardRequest(client, *url, line, method)
		if err != nil {
			log.Printf("[stdio-adapter] Error forwarding request: %v", err)
			// Send error response
			sendError(req, err)
			continue
		}

		// Write response to stdout
		fmt.Println(resp)
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("[stdio-adapter] Error reading stdin: %v", err)
	}

	log.Printf("[stdio-adapter] Shutdown complete")
}

// forwardRequest sends the JSON-RPC request to the HTTP MCP endpoint
func forwardRequest(client *http.Client, url, body, method string) (string, error) {
	// Create HTTP request
	httpReq, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("MCP-Protocol-Version", "2026-07-28")
	httpReq.Header.Set("Mcp-Method", method)

	// Set Mcp-Name header for tools/call
	if method == "tools/call" {
		var req map[string]interface{}
		if err := json.Unmarshal([]byte(body), &req); err == nil {
			if params, ok := req["params"].(map[string]interface{}); ok {
				if name, ok := params["name"].(string); ok {
					httpReq.Header.Set("Mcp-Name", name)
				}
			}
		}
	}

	// Set body
	httpReq.Body = io.NopCloser(strings.NewReader(body))

	// Send request
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP status %d: %s", httpResp.StatusCode, string(respBody))
	}

	return string(respBody), nil
}

// sendError sends a JSON-RPC error response to stdout
func sendError(req map[string]interface{}, err error) {
	id, _ := req["id"]
	errorResp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]interface{}{
			"code":    -32603,
			"message": err.Error(),
		},
	}
	resp, _ := json.Marshal(errorResp)
	fmt.Println(string(resp))
}
