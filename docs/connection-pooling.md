# Connection Pooling Guide

> **Last Updated:** 2026-10-26  
> **Status:** Implemented

## Overview

PostgreSQL connection pooling in the MCP gateway uses Go's built-in `database/sql` pool. This prevents connection exhaustion under high load and improves latency by reusing connections.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `POSTGRES_POOL_MAX_OPEN` | 25 | Maximum open connections |
| `POSTGRES_POOL_MAX_IDLE` | 5 | Maximum idle connections |
| `POSTGRES_POOL_MAX_LIFETIME` | 5m | Maximum connection lifetime |
| `POSTGRES_POOL_MAX_IDLE_TIME` | 1m | Maximum idle time before closing |

## Implementation

### Init with Pool
```go
store.InitWithPool(dsn, maxOpen, maxIdle, maxLifetime, maxIdleTime)
```

### Pool Stats
```go
stats := store.PoolStats()
// stats.MaxOpenConnections, stats.OpenConnections, stats.InUse, stats.Idle
```

## Monitoring

Pool stats are logged at startup:
```
[store] pool configured: maxOpen=25, maxIdle=5, maxLifetime=5m0s, maxIdleTime=1m0s
```

## Files

| File | Purpose |
|------|---------|
| `internal/store/postgres.go` | `InitWithPool()`, `PoolStats()`, pool configuration |
| `internal/config/config.go` | `PostgresPoolConfig`, env loading, validation |