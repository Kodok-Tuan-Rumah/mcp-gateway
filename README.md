# MCP Gateway

> Project boundaries and roadmap: [`PROJECT_CONTEXT.md`](PROJECT_CONTEXT.md) and [`PROJECT_GOALS.md`](PROJECT_GOALS.md). Active checklist: [`TODO.md`](TODO.md).

HTTP orchestration gateway for the AI research workspace.
Go API · ArXiv + RAG answer backends · evaluator status/score · MCP (stdio/SSE/HTTP).

```text
Client → HTTP Gateway :8090 → selected backend → answer + evidence
                                      └→ optional evaluator → status/score

Intended workspace services:
  ArXiv Assistant :8080  |  RAG Research :8000  |  Eval API :8002
```

## Features

- **MCP Protocol** — JSON-RPC 2.0 over HTTP, stdio, and SSE transports
- **Tools:** `search_scientific_literature`, `verify_factual_claim`, `generate_audit_dossier`
- **Auth:** API key via `Authorization: Bearer <key>` or `X-API-Key` (disabled by default)
- **Rate Limiting:** Token bucket per client IP (60 RPM, burst 10)
- **Observability:** Prometheus metrics at `/metrics`, `/health`, `/ready`
- **Audit Logging:** All requests logged to PostgreSQL

## Quickstart

```bash
# Standalone (dev mode, no auth)
go run ./cmd/server

# With auth and rate limiting
GATEWAY_API_KEYS="sk-live-abc123" GATEWAY_RATE_LIMIT_RPM=30 go run ./cmd/server

# stdio transport (for Claude Desktop)
go run ./cmd/server --transport=stdio
```

## Verification

```bash
make check   # Run tests
make demo    # Run demo
```

## Example Response

```json
{
  "request_id": "abc-123",
  "answer": "Multi-head attention projects queries...",
  "evaluation_status": "completed",
  "confidence": 0.85
}
```

## Testing

```bash
go test ./... -race
```

## Documentation

- [`docs/api-contract.md`](docs/api-contract.md) — Public API contract
- [`docs/openapi.yaml`](docs/openapi.yaml) — OpenAPI 3.0 spec (machine-readable)
- [`docs/architecture.md`](docs/architecture.md) — Architecture overview
- [`docs/adr/`](docs/adr/) — Architecture Decision Records (5)