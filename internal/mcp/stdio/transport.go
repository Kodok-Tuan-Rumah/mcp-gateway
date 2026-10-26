package stdio

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"

	"github.com/google/uuid"
	"github.com/malikimayzar/mcp-gateway/internal/mcp"
)

// Transport handles MCP communication over stdio
type Transport struct {
	server *mcp.Server
	reader *bufio.Scanner
	writer io.Writer
	logger *log.Logger
}

// NewTransport creates a new stdio transport
func NewTransport(server *mcp.Server) *Transport {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer size for large messages
	const maxTokenSize = 1024 * 1024
	scanner.Buffer(make([]byte, maxTokenSize), maxTokenSize)
	scanner.Split(bufio.ScanLines)

	return &Transport{
		server: server,
		reader: scanner,
		writer: os.Stdout,
		logger: log.New(os.Stderr, "[mcp-stdio] ", log.LstdFlags),
	}
}

// Start begins reading from stdin and processing requests
func (t *Transport) Start(ctx context.Context) error {
	t.logger.Println("Starting MCP stdio transport")

	for t.reader.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := t.reader.Bytes()
		if len(line) == 0 {
			continue
		}

		t.processLine(ctx, line)
	}

	if err := t.reader.Err(); err != nil {
		t.logger.Printf("Error reading stdin: %v", err)
		return err
	}

	t.logger.Println("Stdin closed, exiting")
	return nil
}

func (t *Transport) processLine(ctx context.Context, line []byte) {
	req, isNotification, jsonErr := parseMessage(line)
	if jsonErr != nil {
		t.writeError(nil, jsonErr)
		return
	}

	// Notifications carry no ID and expect no response
	if isNotification {
		return
	}

	// Generate or extract trace ID
	traceID := uuid.New().String()

	// Handle request
	response, err := t.server.HandleRequest(ctx, req, traceID)
	if err != nil {
		t.logger.Printf("Error handling request: %v", err)
		t.writeError(req.ID, mcp.NewInternalError("internal server error"))
		return
	}

	// Write response
	t.writeResponse(response)
}

func (t *Transport) writeResponse(response []byte) {
	if _, err := t.writer.Write(append(response, '\n')); err != nil {
		t.logger.Printf("Error writing response: %v", err)
	}
}

func (t *Transport) writeError(id interface{}, jsonErr *mcp.JSONRPCError) {
	resp := mcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   jsonErr,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.logger.Printf("Error marshaling error response: %v", err)
		return
	}
	t.writeResponse(data)
}
