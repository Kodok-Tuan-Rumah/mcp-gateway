# MCP Gateway

> Project boundaries and roadmap: [`PROJECT_CONTEXT.md`](PROJECT_CONTEXT.md) and [`PROJECT_GOALS.md`](PROJECT_GOALS.md). Implementation checklist: [`IMPLEMENTATION_TODO.md`](IMPLEMENTATION_TODO.md). Next prioritized tasks: [`TODO_BERIKUTNYA.md`](TODO_BERIKUTNYA.md).

> HTTP orchestration gateway for the AI research workspace.
> Go API · backend adapters · integration work in progress.

```text
Client → HTTP Gateway :8090 → selected backend → answer + evidence
                                      └→ optional evaluator → status/score

Intended workspace services:
  ArXiv Assistant :8080  |  RAG Research :8003  |  Eval API :8002
```

## What This Is

MCP Gateway is intended to be a thin orchestration layer across the four-repository workspace. Its first integration path is still being aligned: the adapters, deployment wiring, and answer/evidence handoff need to match the current backend APIs before the whole pipeline can be considered operational.

The gateway should route and report; retrieval and generation remain owned by the backend repositories. See the project direction documents linked above.

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
| Orchestrator | Groq (default model: openai/gpt-oss-20b) | Fast inference for planning; model configurable via `GROQ_MODEL`, fallback to rule-based if unavailable |
| Evaluation | Ollama (nomic-embed-text) | Local, no LLM judge needed, faithfulness via cosine similarity |
| Storage | PostgreSQL | Eval scores persisted for trend analysis |

---

## Services

| Service | Stack | Port | Role |
|---|---|---|---|
| **mcp-gateway** | Go + chi | 8090 | Orchestration, routing, adapter dispatch |
| **arxiv-research-assistant** | Go + Python + Qdrant | 8080 | ArXiv answer backend (`/query`) — the only active backend |
| **rag-research** | Python + Qdrant + BM25 | 8003 | Research RAG (`/generate`) — **not integrated**; the gateway calls no route here yet |
| **llm-eval-framework** | Python + Ollama | 8002 | Faithfulness evaluation, failure mode analysis |
| **PostgreSQL** | postgres:16-alpine | 5432 | Gateway eval log in its own `gateway_query_logs` table (database owned by `arxiv-research-assistant`) |
| **Qdrant** | qdrant/qdrant | 6333 | Vector store, used by the backend services |
| **Grafana / Prometheus** | — | 3000 / 9090 | Monitoring, owned by `arxiv-research-assistant`; Grafana has no provisioned dashboards yet |

This repository does not start these dependencies. Each has its own deployment
path in its own repository.

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

**Full-stack startup is not ready yet.** This repository does not include a
Compose file connecting the four repositories, and the RAG adapter does not
match `rag-research`'s current API. See [`PROJECT_CONTEXT.md`](PROJECT_CONTEXT.md)
and [`PROJECT_GOALS.md`](PROJECT_GOALS.md) for the integration gates.

A healthy `/health` means the process is alive. It does not mean any downstream
service is reachable — that check does not exist yet.

---

## API

### `POST /ask` — ArXiv answer flow
Groq may select the ArXiv search step; the gateway filters the plan to the selected backend and falls back to a deterministic plan if Groq is unavailable or returns no usable answer step. The gateway currently supports only the `arxiv` backend for answer routes.

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
  "orchestrator": "groq",
  "steps": [
    {"tool_name": "search_arxiv", "success": true},
    {"tool_name": "evaluate_answer", "success": true}
  ]
}
```

The response includes structured `evidence[]` from the same ArXiv result used to build evaluator context. Abstentions return `decision: "abstain"` with `failure_type: "insufficient_context"`; backend failures return `decision: "error"`. If evaluation is skipped or unavailable, that status stays separate from the answer and no score is returned.

### `POST /plan` — Deterministic ArXiv flow
Runs the ArXiv search and evaluation plan without calling Groq. It does not perform automatic retry.

```bash
curl -X POST http://localhost:8090/plan \
  -H "Content-Type: application/json" \
  -d '{"query": "what is BM25?", "top_k": 5}'
```

### `POST /tool` — Direct Tool Call
Call a registered tool directly. This is a low-level passthrough; it is not the normalized answer API. `retrieve_chunks` is registered for visibility but its downstream contract is not implemented and the tool is not used by `/ask` or `/plan`.

```bash
curl -X POST http://localhost:8090/tool \
  -H "Content-Type: application/json" \
  -d '{
    "tool_name": "retrieve_chunks",
    "params": {"query": "transformer architecture", "top_k": 5, "method": "hybrid"}
  }'
```

### `GET /health`
```json
{"check": "liveness", "status": "ok", "tools": ["evaluate_answer", "retrieve_chunks", "search_arxiv"]}
```

---

## Benchmark Results

Measured on local machine (WSL2, CPU only):

| Tool / Endpoint | Avg Latency | Min | Max | Faithfulness |
|---|---|---|---|---|
| `retrieve_chunks` | 113ms | 34ms | 525ms | — |
| `evaluate_answer` | 3.3s | 2.5s | 4.6s | 1.00 |
| `POST /plan` | 57s | 54s | 60s | 0.75 |
| `POST /ask` (Groq) | 76s | 75s | 77s | 0.86 |

> These are historical measurements from the earlier retrieval-based flow. They have not been re-measured against the current ArXiv-only route and should not be used as current gateway performance claims.

---

## Current Evaluation Flow

The answer route attempts evaluation after a successful ArXiv search when answer and source context are available. It skips abstentions and answers shorter than five words because the evaluator's short-answer API currently lacks its required ground truth input. Evaluation is diagnostic evidence support, not proof that an answer is factually correct. An unavailable or skipped evaluation is not stored as a zero score.

```
Query → search_arxiv → answer + source context → evaluate_answer (when possible)
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
│   │   ├── search_arxiv.go     — ArXiv adapter (active answer backend)
│   │   ├── retrieve_chunks.go  — legacy adapter; downstream contract unavailable
│   │   └── evaluate_answer.go  — evaluator adapter
│   ├── planner/                — plan types and sequential execution
│   ├── orchestrator/           — optional Groq plan generation
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

**2. Groq as planner, not generator**  
Groq is only used for *deciding which tools to call* — not for generating the answer. This keeps the system cost-efficient and the answers grounded in retrieved context.

**3. Fallback chain**  
`Groq plan → deterministic plan`. If Groq is unavailable, the key is missing, or
the returned plan contains no usable answer step, the gateway falls back to the
deterministic per-backend plan. There is no per-tool fallback below that:
a backend failure is reported, not papered over.

**4. One backend per request**  
`backendTools` maps each backend to the tools it may call, so a plan can only
contain steps from one list. This is what prevents an ArXiv answer from being
scored against chunks retrieved from a different backend.

**5. Polyglot by necessity, not trend**  
Go handles routing, concurrency, and low-latency HTTP. Python handles ML — embeddings, retrieval, evaluation. Each language does what it's best at.

---

## Environment Variables

All variables are optional; unset values fall back to local-development
defaults. See [`.env.example`](.env.example) for the annotated list — the two
files are kept in sync by a test, so neither can drift silently.

```bash
# Orchestration
GROQ_API_KEY=gsk_...              # without a key, /ask uses the deterministic plan

# Persistence
POSTGRES_DSN=host=localhost ...  # unset or empty → local default DSN; if the DB is unreachable, logging is skipped (answering unaffected)

# Service addresses (gateway appends /query, /evaluate)
ARXIV_SERVICE_URL=http://localhost:8080
EVAL_SERVICE_URL=http://localhost:8002
RAG_SERVICE_URL=http://arxiv-rag-service:8003

# Timeouts and response bounds
GATEWAY_LISTEN_ADDRESS=:8090
GATEWAY_REQUEST_TIMEOUT=620s
STORE_LOG_TIMEOUT=5s
ARXIV_TIMEOUT=600s
EVAL_TIMEOUT=120s
RAG_TIMEOUT=10s
ARXIV_MAX_RESPONSE_BYTES=4194304
EVAL_MAX_RESPONSE_BYTES=1048576
RAG_MAX_RESPONSE_BYTES=4194304
```

Invalid durations or sizes fail startup with an error naming the variable,
rather than silently falling back.

`RAG_SERVICE_URL` is not consumed by the active answer routes. The
`retrieve_chunks` tool remains registered for visibility, but its downstream
API contract is not implemented.

---

## Related Repos

| Repo | Description |
|---|---|
| [arxiv-research-assistant](https://github.com/malikimayzar/arxiv-research-assistant) | Go+Python RAG system, ArXiv ingestion, circuit breaker, observability |
| [llm-eval-framework](https://github.com/malikimayzar/llm-eval-framework) | Faithfulness evaluation without LLM judge |
| [rag-research](https://github.com/malikimayzar/rag-research) | Hybrid retrieval (Qdrant + BM25), confidence engine, retrieval-only evaluation evidence |

---

*Maliki Mayzar · 2026*
