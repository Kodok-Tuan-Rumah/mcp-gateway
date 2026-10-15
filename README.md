# MCP Gateway

> Project boundaries and roadmap: [`PROJECT_CONTEXT.md`](PROJECT_CONTEXT.md) and [`PROJECT_GOALS.md`](PROJECT_GOALS.md). Implementation checklist: [`IMPLEMENTATION_TODO.md`](IMPLEMENTATION_TODO.md). Next prioritized tasks: [`TODO_BERIKUTNYA.md`](TODO_BERIKUTNYA.md).

> HTTP orchestration gateway for the AI research workspace.
> Go API · backend adapters · integration work in progress.

```text
Client → HTTP Gateway :8090 → selected backend → answer + evidence
                                      └→ optional evaluator → status/score

Intended workspace services:
  ArXiv Assistant :8080  |  RAG Research :8000  |  Eval API :8002
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
| ML services | Python | Ecosystem — HuggingFace, FAISS, rank-bm25, sentence-transformers |
| Orchestrator | Groq (llama-3.3-70b) | Fast inference for planning, fallback to rule-based if unavailable |
| Evaluation | Ollama (nomic-embed-text) | Local, no LLM judge needed, faithfulness via cosine similarity |
| Storage | PostgreSQL | Eval scores persisted for trend analysis and Grafana dashboards |

---

## Services

| Service | Stack | Port | Role |
|---|---|---|---|
| **mcp-gateway** | Go + chi | 8090 | Orchestration, tool registry, planner, executor |
| **arxiv-research-assistant** | Go + Python + Qdrant | 8080/8001 | ArXiv search, paper ingestion, RAG |
| **rag-research** | Python + Qdrant + BM25 | 8000 | Research RAG API (`/generate`; gateway adapter integration pending) |
| **llm-eval-framework** | Python + Ollama | 8002 | Faithfulness evaluation, failure mode analysis |
| **PostgreSQL** | postgres:16 | 5432 | Query logs, eval scores, paper metadata |
| **Qdrant** | qdrant/qdrant | 6333 | Vector store for dense retrieval |
| **Grafana** | grafana/grafana | 3000 | Observability dashboard |
| **Prometheus** | prom/prometheus | 9090 | Metrics collection |

---

## Quick Start

**Full-stack startup is not ready yet.** This repository does not currently include a Compose file that connects all four repositories, and the gateway's RAG adapter does not match `rag-research`'s current API. See [`PROJECT_CONTEXT.md`](PROJECT_CONTEXT.md) for the source-level status and [`PROJECT_GOALS.md`](PROJECT_GOALS.md) for the integration gates.

```bash
# From the mcp-gateway repository: start only the gateway process.
go run ./cmd/server

# In another terminal, check that the process is listening.
curl -s http://localhost:8090/health
```

This process check does not verify downstream services or a working `/ask` flow. End-to-end setup will be documented after the integration gates are met.

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

Evaluation results are persisted to `query_logs` table:
```sql
SELECT query, faithfulness, model, retrieval_strategy, created_at
FROM query_logs ORDER BY created_at DESC LIMIT 10;
```

---

## Project Structure

```
mcp-gateway/
├── cmd/server/main.go          — entry point, chi router, /ask /plan /tool
├── internal/
│   ├── api/                    — public request/response contract
│   ├── registry/               — tool registration and dispatch
│   ├── tools/
│   │   ├── search_arxiv.go     — ArXiv adapter (active answer backend)
│   │   ├── retrieve_chunks.go  — legacy adapter; downstream contract unavailable
│   │   └── evaluate_answer.go  — evaluator adapter
│   ├── planner/
│   │   ├── planner.go          — plan types and legacy planner helper
│   │   └── executor.go         — sequential execution and legacy retry helper
│   ├── orchestrator/
│   │   └── groq.go             — optional Groq plan generation
│   └── store/
│       └── postgres.go         — asynchronous evaluation persistence
├── scripts/
│   └── benchmark.py            — latency + faithfulness benchmark
├── docs/tool-contracts/        — JSON specs per tool
└── Makefile                    — start/stop/status/logs
```

---

## Key Engineering Decisions

**1. Evaluation status is explicit**
The current plan attempts evaluation when answer and source context are available. An unavailable or skipped evaluation does not invalidate a backend answer and does not produce a fake zero score.

**2. Groq as planner, not generator**  
Groq is only used for *deciding which tools to call* — not for generating the answer. This keeps the system cost-efficient and the answers grounded in retrieved context.

**3. Fallback chain**  
`Groq plan → rule-based plan → individual tool`. The system degrades gracefully at every layer.

**4. Polyglot by necessity, not trend**  
Go handles routing, concurrency, and low-latency HTTP. Python handles ML — embeddings, retrieval, evaluation. Each language does what it's best at.

---

## Environment Variables

```bash
# Optional; without a key /ask uses the deterministic plan.
GROQ_API_KEY=gsk_...          # Groq API key for plan selection

# Optional (defaults shown)
POSTGRES_DSN=host=localhost port=5432 user=arxiv password=arxiv_secret dbname=arxiv_db sslmode=disable
ARXIV_SERVICE_URL=http://localhost:8080  # base URL; gateway appends /query
EVAL_SERVICE_URL=http://localhost:8002   # base URL; gateway appends /evaluate
```

`RAG_SERVICE_URL` is not consumed by the active answer routes. The `retrieve_chunks` tool remains registered, but its downstream API contract is not implemented.

---

## Related Repos

| Repo | Description |
|---|---|
| [arxiv-research-assistant](https://github.com/malikimayzar/arxiv-research-assistant) | Go+Python RAG system, ArXiv ingestion, circuit breaker, observability |
| [llm-eval-framework](https://github.com/malikimayzar/llm-eval-framework) | Faithfulness evaluation without LLM judge, 105 unit tests |
| [rag-research](https://github.com/malikimayzar/rag-research) | BM25+FAISS hybrid retrieval, ablation study, zero hallucination benchmark |

---

*Maliki Mayzar · 2026*
