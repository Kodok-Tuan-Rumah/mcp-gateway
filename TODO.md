# MCP Gateway — Active Roadmap

> **Snapshot:** 2026-10-26. Single active implementation checklist.
> Historical notes: `TODO_HARI_INI.md`, `TODO_BERIKUTNYA.md` (archived, do not use).

## Completed

- [x] `/ask` and `/plan` contracts with one answer backend per request
- [x] Typed ArXiv (`POST /query`) and RAG (`POST /generate`) adapters
- [x] Normalize answer, evidence, abstention, model/latency fields, and failure states
- [x] Distinguish `completed`, `skipped`, `unavailable` evaluation status
- [x] `/ready` separately from `/health` with per-backend status
- [x] Prometheus metrics on all endpoints
- [x] MCP JSON-RPC 2.0 (HTTP + stdio + SSE transport)
- [x] MCP tools: `search_scientific_literature`, `verify_factual_claim`, `generate_audit_dossier`
- [x] Auth (API key) and rate limiting (token bucket)
- [x] E2E MCP smoke test (16/16 PASS)
- [x] Span-level attribution exposed via MCP
- [x] Confidence calibration exposed via MCP

- [x] OpenTelemetry distributed tracing (gateway → rag → eval)
- [x] Circuit breaker for downstream services
- [x] API key rotation mechanism
- [x] Per-key rate limits (different tiers)
- [x] OpenAPI/Swagger spec for public API
- [x] CI/CD pipeline (GitHub Actions) for gateway
## Pending

- [ ] Deployment guide (`docs/DEPLOYMENT.md`)

## Notes

- `PROJECT_CONTEXT.md` and `PROJECT_GOALS.md` are the source of truth for architecture and goals
- `docs/ROADMAP.md` at workspace root is the consolidated roadmap
- Historical TODO files are archived — do not use them for current work