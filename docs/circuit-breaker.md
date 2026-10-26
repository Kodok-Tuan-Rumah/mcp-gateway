# Circuit Breaker Guide

> **Last Updated:** 2026-10-26  
> **Status:** Implemented

## Overview

The circuit breaker pattern prevents cascade failures when downstream services (arxiv-go-backend, rag-api, llm-eval-framework) become unavailable. Instead of repeatedly timing out on every request, the gateway "trips" the circuit and returns an immediate error, allowing the downstream service time to recover.

## States

```
┌─────────┐  MaxFailures reached  ┌──────┐  Timeout elapsed  ┌───────────┐
│ CLOSED  │ ─────────────────────→ │ OPEN │ ────────────────→ │ HALF-OPEN │
│         │                        │      │                   │           │
│ Normal  │ ←── Probe succeeds ─── │      │ ←── Probe fails ── │  Probe    │
└─────────┘                        └──────┘                   └───────────┘
```

| State | Behavior |
|-------|----------|
| **Closed** | Requests pass through normally. Consecutive failures are counted. |
| **Open** | Requests are rejected immediately with `ErrCircuitOpen`. After `Timeout`, transitions to half-open. |
| **Half-Open** | A single probe request is allowed. Success → closed. Failure → open. |

## Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `MaxFailures` | 5 | Consecutive failures before opening circuit |
| `Timeout` | 30s | How long circuit stays open before half-open |
| `MaxRequests` | 1 | Probe requests allowed in half-open state |

## Usage in Code

```go
breaker := circuitbreaker.GetBreaker("research_rag")
if breaker != nil {
    if err := breaker.Allow(); err != nil {
        return errorResponse(req, fmt.Sprintf("circuit breaker open: %v", err))
    }
}

// Make HTTP call...

if err != nil {
    if breaker != nil { breaker.RecordFailure() }
    return errorResponse(req, err.Error())
}
if breaker != nil { breaker.RecordSuccess() }
```

## Monitoring

Circuit breaker states are exposed via `/ready` endpoint:

```json
{
  "status": "ready",
  "circuit_breakers": {
    "arxiv": "closed",
    "research_rag": "closed",
    "evaluate_answer": "open"
  }
}
```

## Files

| File | Purpose |
|------|---------|
| `internal/circuitbreaker/breaker.go` | Core implementation (Breaker, Manager, Config) |
| `internal/circuitbreaker/breaker_test.go` | 8 test cases |
| `internal/tools/research_rag.go` | Circuit breaker integrated |
| `internal/tools/search_arxiv.go` | Circuit breaker integrated |
| `internal/tools/evaluate_answer.go` | Circuit breaker integrated |
| `cmd/server/router.go` | Circuit breaker states in `/ready` |