# Graceful Shutdown Guide

> **Last Updated:** 2026-10-26  
> **Status:** Implemented

## Overview

Graceful shutdown ensures that in-flight requests complete before the service exits. This prevents connection leaks, incomplete database writes, and data corruption during container restarts.

## Implementation

### Go (mcp-gateway)

The gateway uses `http.Server.Shutdown()` with a configurable timeout:

1. **Signal handling:** Listens for `SIGINT` (Ctrl+C) and `SIGTERM` (Docker stop)
2. **Graceful shutdown:** Calls `srv.Shutdown(ctx)` with 30s timeout
3. **Circuit breaker reset:** Resets all circuit breakers to closed state
4. **Tracing shutdown:** Flushes pending spans to OTLP collector

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
sig := <-quit

shutdownCtx, cancel := context.WithTimeout(context.Background(), DefaultShutdownTimeout)
defer cancel()

if err := srv.Shutdown(shutdownCtx); err != nil {
    logger.Error("Graceful shutdown failed", logging.Fields{"error": err.Error()})
    log.Fatal(err)
}
```

### Python (rag-research)

FastAPI lifespan context manager handles shutdown:

```python
yield

# Shutdown tracing
shutdown_tracing()

if store:
    logger.info("[SHUTDOWN] Closing Vector Store connections...")
    store.close()
```

## Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `DefaultShutdownTimeout` | 30s | Max time to wait for in-flight requests |

## Behavior

1. On `SIGTERM`/`SIGINT`, the server stops accepting new connections
2. In-flight requests have up to 30s to complete
3. After timeout, remaining connections are forcefully closed
4. Circuit breakers are reset
5. Tracing spans are flushed
6. Database connections are closed

## Docker Integration

Docker sends `SIGTERM` on `docker stop` with a default 10s timeout. For graceful shutdown to work properly:

```bash
# Increase Docker stop timeout
docker compose up -d --stop-timeout 35
```

Or set in `docker-compose.yml`:
```yaml
services:
  mcp-gateway:
    stop_grace_period: 35s
```

## Files

| File | Purpose |
|------|---------|
| `mcp-gateway/cmd/server/main.go` | Signal handling + graceful shutdown |
| `rag-research/src/api/main.py` | Lifespan shutdown hook |