# MCP Gateway

> Project boundaries and roadmap: [`PROJECT_CONTEXT.md`](PROJECT_CONTEXT.md) and [`PROJECT_GOALS.md`](PROJECT_GOALS.md). The single active implementation checklist is [`IMPLEMENTATION_TODO.md`](IMPLEMENTATION_TODO.md). `TODO_HARI_INI.md`, `TODO_BERIKUTNYA.md`, and `docs/workspace-alignment.md` are historical notes.

> HTTP orchestration gateway for the AI research workspace.
> Go API · ArXiv + RAG answer backends · evaluator status/score.

```text
Client → HTTP Gateway :8090 → selected backend → answer + evidence
                                      └→ optional evaluator → status/score

Intended workspace services:
  ArXiv Assistant :8080  |  RAG Research :8000  |  Eval API :8002
```

## What This Is

MCP Gateway is a thin orchestration layer across the four-repository workspace. Both answer backends are wired: `arxiv` (`POST /query`) and `research_rag` (`POST /generate`). Answer, evidence, and the evaluator context are all taken from the selected backend, so evidence never crosses backends. The workspace root compose starts the four repositories and their shared infrastructure together.

The gateway routes and reports; retrieval and generation remain owned by the backend repositories. Root Compose defines the combined topology, but that definition alone is not proof of a currently healthy deployment. See the active roadmap for runtime verification gates.

Jawaban pendek tetap dikirim ke evaluator. Untuk request online tanpa
`ground_truth`, evaluator memakai exact match konservatif terhadap context
evidence. Gateway melewati evaluasi untuk abstensi atau context kosong.

---

## Interface and Architecture Direction

| Decision | Choice | Reason |
|---|---|---|
| External API | HTTP/JSON (`/ask`, `/plan`, `/tool`) | Current implementation; MCP protocol interoperability is not implemented yet |
| Gateway language | Go | Low latency routing, goroutine-based async logging, strong typing |
| ML services | Python | Ecosystem — HuggingFace, sentence-transformers, rank-bm25 |
| Orchestrator | Deterministic backend plan | One selected answer backend followed by evaluation; no external planner dependency |
| Evaluation | Ollama (nomic-embed-text) | Local, no LLM judge needed, faithfulness via cosine similarity |
| Storage | PostgreSQL | Eval scores persisted for trend analysis |

---

## Services

| Service | Stack | Port | Role |
|---|---|---|---|
| **mcp-gateway** | Go + chi | 8090 | Orchestration, routing, adapter dispatch |
| **arxiv-research-assistant** | Go + Python + Qdrant | 8080 | ArXiv answer backend (`/query`) — active backend |
| **rag-research** | Python + Qdrant + BM25 | 8000 | Research RAG (`/generate`) — active backend via `research_rag` |
| **llm-eval-framework** | Python + Ollama | 8002 | Faithfulness evaluation, failure mode analysis |
| **PostgreSQL** | postgres:16-alpine | 5432 | Gateway eval log in its own `gateway_query_logs` table (database owned by `arxiv-research-assistant`) |
| **Qdrant** | qdrant/qdrant | 6333 | Vector store, used by the backend services |
| **Grafana / Prometheus** | — | 3000 / 9090 | Monitoring; the workspace root provisions the Prometheus datasource and overview dashboard |

This repository does not start these dependencies itself. The workspace root
compose starts them, and each project also has its own deployment path.

---

## Quick Start

```bash
make check   # build + vet + test -race + lint
make demo    # start the gateway and exercise the endpoints
```

`make demo` needs no downstream services. It starts the real gateway, runs the
routing and failure cases, and shuts down. This is the fastest way to confirm
behaviour: it shows that an unavailable backend produces `502` with an empty
answer rather than a plausible-looking one.

To run it yourself:

```bash
go run ./cmd/server
# another terminal
curl -s http://localhost:8090/health
```

Full-stack startup is owned by the workspace root compose (`make up` from the
workspace root), which starts the four repositories together. This repository's
`make demo` still runs the gateway standalone without downstream services.

A healthy `/health` means the process is alive. Use `/ready` for dependency
state: it probes the configured ArXiv, evaluator, and RAG endpoints concurrently
and returns `503` when any probe is not `up`.

---

## API

### `POST /ask` — answer flow
Runs one deterministic plan for the selected backend: `search_arxiv` or `research_rag`, followed by `evaluate_answer` when answer and evidence are available. If omitted, `backend` defaults to `arxiv`.

```bash
curl -X POST http://localhost:8090/ask \
  -H "Content-Type: application/json" \
  -d '{"query": "what is attention mechanism?", "top_k": 5}'
```

```json
{
  "request_id": "...",
  "query": "what is attention mechanism?",
  "answer": "...",
  "backend": "arxiv",
  "decision": "answer",
  "failure_type": "none",
  "evaluation_status": "completed",
  "evaluation": {"score": 0.8611, "version": "1.2.1"},
  "orchestrator": "rule-based",
  "steps": [
    {"tool_name": "search_arxiv", "success": true},
    {"tool_name": "evaluate_answer", "success": true}
  ]
}
```

The response includes structured `evidence[]` from the selected backend result; the same evidence builds evaluator context. Abstentions return `decision: "abstain"` with `failure_type: "insufficient_context"`; backend failures return `decision: "error"`. If evaluation is skipped or unavailable, that status stays separate from the answer and no score is returned.

### `POST /plan` — compatibility alias
Runs the same answer flow and returns the same response contract as `/ask`. The route is kept for existing clients; it does not return a preview-only plan.

```bash
curl -X POST http://localhost:8090/plan \
  -H "Content-Type: application/json" \
  -d '{"query": "what is BM25?", "top_k": 5}'
```

### `POST /tool` — Direct Tool Call
Call a registered tool directly. This is a low-level passthrough; it is not the normalized answer API. Normal clients should use `/ask` so backend selection, evidence normalization, evaluation, and failure mapping stay consistent.

```bash
curl -X POST http://localhost:8090/tool \
  -H "Content-Type: application/json" \
  -d '{
    "tool_name": "research_rag",
    "params": {"query": "transformer architecture", "top_k": 5, "use_reranker": false}
  }'
```

### `GET /health`
```json
{"check": "liveness", "status": "ok", "tools": ["evaluate_answer", "research_rag", "search_arxiv"]}
```

### `GET /ready`
Probes the configured ArXiv, evaluator, and RAG `/health` endpoints concurrently.
The response includes each service and the answer capabilities available now.
It returns HTTP 200 when at least one answer backend is reachable, and HTTP 503
when both answer backends are unavailable. An unavailable evaluator marks the
gateway `degraded` but does not block answering.

```json
{"check":"readiness","status":"degraded","services":{"arxiv":"up","evaluator":"down","rag":"up"},"capabilities":{"answers":true,"arxiv":true,"research_rag":true}}
```

An HTTP failure from a service is reported as `degraded`; an unreachable
service is `down`. The evaluator entry reflects its `/health` endpoint, so it
shows process reachability rather than confirming its embedding dependency is
ready. A real request reports evaluator availability through `evaluation_status`.

---

## Benchmark Results

Measured on local machine (WSL2, CPU only):

| Tool / Endpoint | Avg Latency | Min | Max | Faithfulness |
|---|---|---|---|---|
| `evaluate_answer` | 3.3s | 2.5s | 4.6s | 1.00 |
| `POST /plan` | 57s | 54s | 60s | 0.75 |
| `POST /ask` (earlier Groq planner) | 76s | 75s | 77s | 0.86 |

> These are historical measurements from an earlier flow. They have not been re-measured against the current two-backend route and should not be used as current gateway performance claims.

[`scripts/benchmark.py`](scripts/benchmark.py) measures `/ask` end to end for
both backends and writes the run data to /tmp/benchmark_results.json.

---

## Current Evaluation Flow

The answer route attempts evaluation after a successful selected backend call when answer and source context are available. It skips abstentions (backend sentinels, `insufficient_context`, or RAG `REJECT`). Evaluation is diagnostic evidence support, not proof that an answer is factually correct. An unavailable or skipped evaluation is not stored as a zero score.

### `evaluation_status` semantics

`evaluation_status` is always present and is never inferred from a score — a legitimate `0` and "not evaluated" are different states. Each answer flow places an evaluation step after the answering step; what differs is how that step ends:

| Status | Meaning | Triggers |
|---|---|---|
| `skipped` | By design, there was nothing to evaluate. No score is returned. | The answering step produced no answer/context, or the backend **abstained** — a gate rejection is an intentional outcome, not a failure. |
| `unavailable` | Evaluation was planned, but the evaluator could not produce a trustworthy score. **The answer stays valid** — `failure_type` is not altered. | Evaluator unreachable or exceeding its timeout (default 120s), non-200 response, malformed body, or a success response without a parseable score. |
| `completed` | The evaluator ran and returned a valid score. | `evaluation.score` is filled, with optional `failure_mode` (`none`, `unsupported_claims`, `insufficient_context`) and `evaluator_version`. |

Worked example (live workspace behavior): an `/ask` with `backend: research_rag` whose quality gate rejects the generated text returns `decision: "abstain"` together with `evaluation_status: "skipped"` — correct, because an abstention has nothing to grade and is not an error.

```
Query → selected backend → answer + source context → evaluate_answer (when possible)
                                                     │
                                        status + optional score in response
                                                     │
                                    persist only valid scores asynchronously
```

Evaluation results are persisted to the gateway's own `gateway_query_logs` table:
```sql
SELECT request_id, query, decision, evaluation_status, faithfulness, backend_model, duration_ms, created_at
FROM gateway_query_logs ORDER BY created_at DESC LIMIT 10;
```

---

## Project Structure

```
mcp-gateway/
├── cmd/server/
│   ├── main.go                 — bootstrap: config, store, registry, listen
│   ├── router.go               — HTTP handler + request/response helpers
│   ├── plan.go                 — per-backend plan construction
│   └── respond.go              — execution result → public contract
├── internal/
│   ├── api/                    — public request/response contract + golden files
│   ├── config/                 — startup configuration from environment
│   ├── registry/               — tool registration and dispatch
│   ├── tools/
│   │   ├── arxiv_contract.go   — typed ArXiv response + evidence mapping
│   │   ├── evaluate_contract.go— typed evaluator response
│   │   ├── search_arxiv.go     — ArXiv answer adapter
│   │   ├── research_rag.go     — RAG `/generate` answer adapter
│   │   └── evaluate_answer.go  — evaluator adapter
│   ├── planner/                — plan types and sequential execution
│   └── store/                  — asynchronous evaluation persistence
├── scripts/
│   ├── demo.sh                 — start gateway + exercise endpoints (make demo)
│   └── benchmark.py            — latency benchmark (needs downstream services)
├── docs/
│   ├── api-contract.md         — normative API specification
│   └── tool-contracts/         — per-adapter JSON contracts
└── Makefile                    — check / demo / start / stop / status
```

---

## Key Engineering Decisions

**1. Evaluation status is explicit**
The current plan attempts evaluation when answer and source context are available. An unavailable or skipped evaluation does not invalidate a backend answer and does not produce a fake zero score.

**2. Keep planning deterministic**
The request's selected backend maps to one answer adapter and one evaluator step. This keeps routing predictable and removes an external planner call from the critical path.

**3. Report backend failure directly**
The gateway does not switch to another answer backend after a failure. It returns the selected backend's failure so the caller can make an informed retry or routing decision.

**4. One backend per request**  
`planForBackend` selects exactly one answer tool, preventing an ArXiv answer from being scored against evidence retrieved by RAG, or vice versa.

**5. Polyglot by necessity, not trend**  
Go handles routing, concurrency, and low-latency HTTP. Python handles ML — embeddings, retrieval, evaluation. Each language does what it's best at.

---

## Environment Variables

All variables are optional; unset values fall back to local-development
defaults. See [`.env.example`](.env.example) for the annotated list — the two
files are kept in sync by a test, so neither can drift silently.

```bash
# Persistence
POSTGRES_DSN=host=localhost ...  # unset or empty → local default DSN; if the DB is unreachable, logging is skipped (answering unaffected)

# Service addresses (adapter paths: /query, /generate, /evaluate)
ARXIV_SERVICE_URL=http://localhost:8080
EVAL_SERVICE_URL=http://localhost:8002
RAG_SERVICE_URL=http://rag-api:8000

# Timeouts and response bounds
GATEWAY_LISTEN_ADDRESS=:8090
GATEWAY_REQUEST_TIMEOUT=620s
STORE_LOG_TIMEOUT=5s
ARXIV_TIMEOUT=600s
EVAL_TIMEOUT=120s
RAG_TIMEOUT=60s
ARXIV_MAX_RESPONSE_BYTES=4194304
EVAL_MAX_RESPONSE_BYTES=1048576
RAG_MAX_RESPONSE_BYTES=4194304
```

Invalid durations or sizes fail startup with an error naming the variable,
rather than silently falling back.

`RAG_SERVICE_URL` is consumed by the active `research_rag` adapter, which calls
the supported `POST /generate` endpoint.

---

## Related Repos

| Repo | Description |
|---|---|
| [arxiv-research-assistant](https://github.com/malikimayzar/arxiv-research-assistant) | Go+Python RAG system, ArXiv ingestion, circuit breaker, observability |
| [llm-eval-framework](https://github.com/malikimayzar/llm-eval-framework) | Faithfulness evaluation without LLM judge |
| [rag-research](https://github.com/malikimayzar/rag-research) | Hybrid retrieval (Qdrant + BM25), confidence engine, retrieval-only evaluation evidence |

---

*Maliki Mayzar · 2026*
