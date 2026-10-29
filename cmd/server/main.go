// Package main adalah entrypoint gateway.
//
// Seluruh wiring HTTP ada di router.go, pemetaan ke kontrak publik di
// respond.go, dan penyusunan plan di plan.go — supaya semuanya bisa diuji
// tanpa menjalankan proses server.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/circuitbreaker"
	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/logging"
	"github.com/malikimayzar/mcp-gateway/internal/mcp"
	"github.com/malikimayzar/mcp-gateway/internal/mcp/stdio"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
	"github.com/malikimayzar/mcp-gateway/internal/store"
	"github.com/malikimayzar/mcp-gateway/internal/tools"
	"github.com/malikimayzar/mcp-gateway/internal/tracing"
)

// DefaultShutdownTimeout is the maximum time to wait for in-flight requests
// to complete during graceful shutdown.
const DefaultShutdownTimeout = 30 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid runtime configuration: %v", err)
	}

	// Initialize structured logger
	logger := logging.NewLogger("mcp-gateway", logging.INFO)

	store.InitWithPool(cfg.PostgresDSN, cfg.PostgresPool.MaxOpenConns, cfg.PostgresPool.MaxIdleConns, cfg.PostgresPool.ConnMaxLifetime, cfg.PostgresPool.ConnMaxIdleTime)
	defer store.Close()

	reg := newRegistry(cfg)

	// Initialize MCP server
	mcpServer := mcp.NewServer(mcp.ServerInfo{
		Name:    "mcp-gateway",
		Version: "1.0.0",
	})
	mcp.RegisterGatewayTools(mcpServer, cfg)

	// Check transport mode
	transport := os.Getenv("MCP_TRANSPORT")
	if transport == "" {
		// Check for --transport flag
		for i, arg := range os.Args {
			if arg == "--transport" && i+1 < len(os.Args) {
				transport = os.Args[i+1]
				break
			} else if strings.HasPrefix(arg, "--transport=") {
				transport = strings.TrimPrefix(arg, "--transport=")
				break
			}
		}
	}
	if transport == "" {
		transport = "http"
	}

	// Initialize tracing
	tracingCfg := tracing.LoadConfig()
	shutdownTracer, err := tracing.InitTracer(tracingCfg)
	if err != nil {
		logger.Info("Tracing init failed", logging.Fields{"error": err.Error()})
	} else {
		defer shutdownTracer(context.Background())
	}
	if tracingCfg.Enabled {
		logger.Info("Tracing enabled", logging.Fields{"endpoint": tracingCfg.OTLPEndpoint})
	}

	logger.Info("MCP Gateway starting", logging.Fields{
		"listen_address": cfg.ListenAddress,
		"postgres_dsn":    maskDSN(cfg.PostgresDSN),
		"mcp_enabled":     true,
		"transport":       transport,
	})

	if transport == "stdio" {
		// Run in stdio mode
		stdioTransport := stdio.NewTransport(mcpServer)
		if err := stdioTransport.Start(context.Background()); err != nil {
			logger.Error("Stdio transport failed", logging.Fields{"error": err.Error()})
			log.Fatal(err)
		}
		return
	}

	// Create HTTP server with graceful shutdown support
	handler := newRouterWithConfigAndMCP(reg, cfg, mcpServer)
	srv := &http.Server{
		Addr:    cfg.ListenAddress,
		Handler: handler,
	}

	// Start server in goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server failed to start", logging.Fields{"error": err.Error()})
			log.Fatal(err)
		}
	}()

	logger.Info("MCP Gateway ready", logging.Fields{"listen_address": cfg.ListenAddress})

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Info("Shutdown signal received", logging.Fields{"signal": sig.String()})

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), DefaultShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Graceful shutdown failed", logging.Fields{"error": err.Error()})
		log.Fatal(err)
	}

	// Reset circuit breakers
	if cbm := circuitbreaker.GlobalManager(); cbm != nil {
		cbm.ResetAll()
	}

	logger.Info("MCP Gateway stopped gracefully", logging.Fields{})
}

// maskDSN masks password in DSN for logging
func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	// Mask the password value so credentials are never written to logs.
	return regexp.MustCompile(`(?i)(password=)[^ ]*`).ReplaceAllString(dsn, "${1}***")
}

// newRegistry mendaftarkan adapter yang didukung gateway.
func newRegistry(cfg config.Config) *registry.Registry {
	reg := registry.New()
	// Initialize circuit breaker manager
	cbCfg := circuitbreaker.DefaultConfig()
	cbManager := circuitbreaker.NewManager(cbCfg)
	circuitbreaker.SetGlobalManager(cbManager)
	reg.Register("search_arxiv", tools.NewSearchArxiv(cfg.Arxiv))
	reg.Register("research_rag", tools.NewResearchRAG(cfg.RAG))
	reg.Register("evaluate_answer", tools.NewEvaluateAnswer(cfg.Evaluator))
	return reg
}

// evalWriter adalah fungsi penulisan log evaluasi (store.LogEval), di-inject
// supaya logEvalAsync bisa diuji tanpa PostgreSQL.
type evalWriter func(ctx context.Context, e store.EvalLog)