# MCP Gateway — Active Roadmap

> **Snapshot:** 2026-10-23. This is the single active implementation checklist.
> “Done” below means source-level implementation or recorded local evidence; it
> does not imply a fresh runtime verification in this session.

Project boundaries and current source behavior are in
[`PROJECT_CONTEXT.md`](PROJECT_CONTEXT.md). Public API shapes are in
[`docs/api-contract.md`](docs/api-contract.md). The Phase 0 alignment notes are
historical; use this roadmap for current work.

## Completed source-level work

- [x] Define `/ask` and `/plan` contracts and select one answer backend per request.
- [x] Use one deterministic execution flow for `/ask` and the `/plan` compatibility alias; remove the unused Groq planning dependency.
- [x] Implement typed ArXiv (`POST /query`) and RAG (`POST /generate`) adapters.
- [x] Normalize answer, evidence, abstention, model/latency fields, and failure states.
- [x] Pass the selected backend's evidence context to the optional evaluator.
- [x] Distinguish `completed`, `skipped`, and `unavailable`; never invent a score when evaluation fails.
- [x] Add `/ready` separately from `/health` and expose Prometheus metrics.
- [x] Add adapter and routing contract tests with downstream stubs, as recorded in repo history/docs.

## P0 — Make the shared deployment reproducible

- [ ] Run the documented root Compose path from a clean environment and record image build, service health, migrations, volumes, and shutdown behavior.
- [ ] Verify each Prometheus target and the Grafana dashboard against a live stack; record date and output.
- [ ] Make the smoke check prove a fresh run wrote its own ArXiv query log (use a unique request/run identifier; do not rely only on a fixed query string).
- [x] Define readiness per capability: `/ready` reports each answer backend and returns HTTP 200 if at least one is reachable; evaluator failure is degraded but does not block answers.
- [ ] Test evaluator readiness after its embedding dependency is initialized, not only process liveness. The gateway currently probes evaluator `/health`; a deeper signal must come from `llm-eval-framework`.

## P1 — Security, tracing, and operational behavior

- [ ] Propagate the gateway request ID to downstream services and include it in their logs where supported.
- [ ] Define authentication, rate limits, and an allowlist for public `/tool` calls before any shared-network exposure.
- [ ] Replace development fallback credentials for PostgreSQL/Grafana in non-local environments; document secret injection and rotation.
- [ ] Define whether query, answer, and evidence text may be persisted; document redaction and retention.
- [ ] Make persistence failures observable without blocking a valid answer or logging sensitive payloads.
- [x] Retire the legacy `retrieve_chunks` adapter because `rag-research` does not expose its `/retrieve` endpoint; `research_rag` remains the supported adapter through `/generate`.

## P2 — Quality evidence and release claims

- [ ] Add gateway-level acceptance cases for both backends, abstention, evaluator outage, malformed downstream responses, provenance, and persistence.
- [ ] Measure end-to-end latency and provider cost on a versioned workload; keep those figures separate from retrieval-only timings.
- [ ] Use a reviewed dataset with enough coverage before making numeric claims about faithfulness, abstention accuracy, or calibration.
- [ ] Keep the gateway described as HTTP/JSON until MCP transport and client interoperability are implemented and verified.
- [ ] Update README, API docs, and this roadmap together whenever a release gate changes.

## Deferred decisions

- Retry policy remains disabled until retryable failure classes, changed inputs, limits, and attempt logging are specified.
- MCP wire protocol support is a product decision, not implied by the repository name or its tool registry.
- Cross-backend answer synthesis is out of scope until provenance and evaluation semantics are designed explicitly.
