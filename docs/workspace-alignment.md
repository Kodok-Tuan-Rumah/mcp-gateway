# Workspace Contract Alignment — Phase 0

> **Review date:** 2026-10-15  
> **Scope:** source and deployment definitions in `arxiv-research-assistant`, `rag-research`, `llm-eval-framework`, and `mcp-gateway`.  
> **Evidence type:** static source review. No services were started and no cross-repo request was executed.

## Goal

Agree on who owns each capability and on the request/response seams before adding a second backend or describing the four repos as one runnable platform.

## Product boundary decisions

| Capability | Owner | Gateway responsibility |
|---|---|---|
| ArXiv ingestion, indexed paper corpus, ArXiv answer generation | `arxiv-research-assistant` | Select this backend for paper-corpus questions; preserve its answer and sources |
| General research corpus, retrieval, confidence/abstention, grounded generation | `rag-research` | Select this backend for general-corpus questions; preserve its answer and retrieved chunks |
| Faithfulness and robustness evaluation | `llm-eval-framework` | Optionally submit one backend's answer and its own evidence; report evaluator availability separately |
| Public entry point, backend selection, normalization, tracing, failure mapping | `mcp-gateway` | Route one request to one answer backend; never merge backend answers or evidence in v1 |

These boundaries follow [`../WORKSPACE_DIRECTION.md`](../WORKSPACE_DIRECTION.md). The target contract requires explicit client selection between `arxiv` and `research_rag`; the current gateway default of `arxiv` is transitional and must be removed before the coherent two-backend release.

## Source-verified service contracts

### ArXiv Assistant

- Go API: `POST /query` on port `8080` (`go-backend/internal/api/query.go`).
- Request accepted by service: `query`, `top_k`, `model`, optional `arxiv_id`. The service defaults `top_k` to 5 and `model` to `phi3:mini`.
- Response: `answer`, `sources[{text, score, arxiv_id}]`, `retrieval_ms`, `generation_ms`, and `model`.
- Abstention is currently encoded as the answer text `Not found in context.`; there is no structured decision/failure field.
- The gateway adapter sends `query` and `top_k`, which is valid because model has a service default. It must normalize each source into `api.Evidence` and map the sentinel to `decision=abstain`.
- The gateway's `ARXIV_SERVICE_URL` is a base URL; it appends `/query`. The Go backend itself uses `ML_SERVICE_URL` for the Python ML service.

### RAG Research

- FastAPI exposes `POST /generate` on port `8000`, not `/retrieve` (`rag-research/src/api/main.py`, `Dockerfile`, `docker-compose.yml`).
- Request: `query` (3–1000 chars), optional `top_k` (1–20), optional `use_reranker` (default false).
- Response includes `answer`, `retrieved_chunks[{chunk_id, doc_id, text, score, metadata, ranking_mode}]`, retrieval/generation/total latency, `model`, confidence, retry information, `decision`, `failure_type`, and abstention metadata.
- `decision` values include `REJECT`, `PARTIAL_TRUST`, and `GENERATE`. A response can also abstain after generation via `generator_status=INSUFFICIENT_CONTEXT`; adapter mapping must inspect both fields rather than treating every HTTP 200 as an answer.
- The current gateway `retrieve_chunks` adapter uses `RAG_SERVICE_URL` (default port `8000`) and calls `POST /retrieve`, which this API does not expose. It is not an adapter for `/generate` and must stay out of `/ask` and `/plan`; the `research_rag` tool is the answer adapter.

### Evaluation Framework

- FastAPI exposes `POST /evaluate` on port `8002`.
- Request: `answer`, `context`, `question`. Response: `faithfulness_score`, `failure_mode`, `has_failure`, `supported_claims`, `total_claims`, `evaluator_version`.
- `FaithfulnessEvaluator` sends answers shorter than the configured word threshold (default 5) through `_evaluate_short_answer`. When the HTTP request has no `ground_truth`, it now uses a conservative whole-word/phrase match against the supplied evidence instead of matching every answer against the empty string.
- The ArXiv abstention sentinel is four words and still enters that short-answer path. The gateway skips evaluation for abstentions, so the abstention itself is not scored as a normal answer.
- Evaluator container configuration must provide a reachable `OLLAMA_HOST`; its default `localhost:11434` points inside the evaluator container.

## Target request flow

```text
client chooses backend
  → gateway validates request and routes to exactly one answer backend
  → selected backend returns answer/abstention + its evidence + provenance
  → gateway normalizes response while preserving backend semantics
  → optional evaluator receives that answer and that evidence only
  → response returns request ID, backend decision, evidence, and evaluation status
```

The first integration vertical slice remains ArXiv because its `/query` response already contains an answer and sources. RAG is a distinct subsequent adapter using `/generate`. Do not use RAG retrieval chunks as context for an ArXiv answer.

## Deployment alignment

- ArXiv infra Compose defines PostgreSQL/Qdrant/monitoring, but not the Go API or Python ML app as a complete runnable stack.
- RAG Compose defines its API as `rag-api:8000` on a repository-local network and Qdrant/Ollama for that repo.
- The evaluator Dockerfile exposes `8002`; its Ollama endpoint is configured separately.
- `mcp-gateway` owns the future shared deployment definition because service discovery and cross-service configuration belong to the gateway boundary; the proposed location is `mcp-gateway/deploy/`.
- This gateway repo has no deployment definition joining those networks and services. Localhost defaults work only when services are reachable from the gateway process on the host. Container deployment needs a shared network and explicit `ARXIV_SERVICE_URL`, `EVAL_SERVICE_URL`, `RAG_SERVICE_URL`, and `OLLAMA_HOST` configuration.
- No cross-repo Compose setup or endpoint was runtime-verified in this review.

## Phase 0 exit checklist

- [x] Establish one owner per capability and keep the two answer engines separate.
- [x] Select ArXiv as the first end-to-end adapter path; add RAG as a separate later adapter.
- [x] Identify actual request/response shapes and the existing `/retrieve` mismatch.
- [x] Identify evaluator short-answer/abstention semantics as unsafe for gateway decisions.
- [x] Make explicit client backend selection the target for the coherent multi-backend release; treat the current ArXiv default as transitional.
- [x] Constrain the gateway from sending abstentions and short answers into the evaluator's unsafe path.
- [ ] Correct the evaluator API semantics and expose evaluator version before treating all scores as comparable.
- [x] Assign the shared deployment definition to `mcp-gateway`; proposed location is `deploy/`.

Phase 0 source alignment is complete: ownership, target routing, current contracts, and deployment ownership are recorded. The evaluator implementation/version gate remains before its scores can be treated as comparable. Proceed with the ArXiv Phase 1 adapter work; do not enable the RAG route or rely on short-answer evaluator scores until the relevant gates close.
