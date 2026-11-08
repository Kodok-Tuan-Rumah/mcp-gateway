# MCP Gateway — TODO

> **Status:** Semua item completed. File ini dipertahankan sebagai catatan historis.
>
> Satu-satunya item pending yang tersisa:
> - **`docs/DEPLOYMENT.md`** — terdaftar sebagai task N3 di [`docs/ai-taskboard.md`](../docs/ai-taskboard.md).
>
> Untuk roadmap aktif, lihat [`docs/ROADMAP.md`](../docs/ROADMAP.md).
> Untuk arsitektur gateway, lihat [`PROJECT_GOALS.md`](PROJECT_GOALS.md) dan [`PROJECT_CONTEXT.md`](PROJECT_CONTEXT.md).

## Completed (Arsip)

Semua fitur berikut sudah diimplementasi dan diverifikasi (Phase 2 & 3):

- `/ask`, `/plan` contracts dengan typed adapters (arxiv, research_rag)
- `/ready` dengan per-backend status
- Prometheus metrics semua endpoint
- MCP JSON-RPC 2.0 (HTTP + stdio + SSE transport)
- MCP tools: `search_scientific_literature`, `verify_factual_claim`, `generate_audit_dossier`
- Auth (API key) + rate limiting (token bucket)
- E2E MCP smoke test 16/16 PASS
- Span-level attribution & confidence calibration via MCP
- OpenTelemetry distributed tracing
- Circuit breaker per downstream
- API key rotation runtime
- Per-key rate limits (free/pro/enterprise tiers)
- OpenAPI/Swagger spec
- CI/CD pipeline (GitHub Actions)
