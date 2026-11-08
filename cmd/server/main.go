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

	"github.com/malikimayzar/mcp-gateway/internal/admin"
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

	// Initialize bounded audit log writer so eval logs are durably queued and
	// can be drained on shutdown instead of being lost.
	globalAuditWriter = NewAuditWriter(1000, 4)
	defer globalAuditWriter.Close()

	reg := newRegistry(cfg)

	// Create a single shared API key store for both public and admin routers.
	// This ensures tenant IDs are stable across routers and key rotations
	// affect both auth and admin endpoints.
	keyStore := newKeyStore(cfg)
	admin.SetGlobalKeyStore(keyStore)

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

	// Create public HTTP server with graceful shutdown support
	handler := newRouterWithConfigAndMCP(reg, cfg, mcpServer, keyStore)
	srv := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Create isolated admin server (bound to 127.0.0.1 by default). /admin/*
	// routes live here and are not reachable from the public network.
	adminHandler := newAdminRouter(reg, cfg, keyStore)
	adminSrv := &http.Server{
		Addr:              cfg.AdminListenAddress,
		Handler:           adminHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Start both servers; report startup failures through a shared channel.
	startErr := make(chan error, 2)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			startErr <- err
		}
	}()
	go func() {
		if err := adminSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			startErr <- err
		}
	}()

	// Surface any immediate bind errors; otherwise assume servers are ready.
	select {
	case err := <-startErr:
		if err != nil {
			logger.Error("Server failed to start", logging.Fields{"error": err.Error()})
			log.Fatal(err)
		}
	default:
	}

	logger.Info("MCP Gateway ready", logging.Fields{
		"listen_address":     cfg.ListenAddress,
		"admin_listen_address": cfg.AdminListenAddress,
	})

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Info("Shutdown signal received", logging.Fields{"signal": sig.String()})

	// Drain pending audit/eval log writes before shutting down servers, so no
	// execution evidence is lost.
	if globalAuditWriter != nil {
		globalAuditWriter.Drain()
	}

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), DefaultShutdownTimeout)
	defer cancel()

	var shutdownErr error
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Public server graceful shutdown failed", logging.Fields{"error": err.Error()})
		shutdownErr = err
	}
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Admin server graceful shutdown failed", logging.Fields{"error": err.Error()})
		shutdownErr = err
	}
	if shutdownErr != nil {
		log.Fatal(shutdownErr)
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