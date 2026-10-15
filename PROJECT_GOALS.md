# MCP Gateway — Project Goals & Integration Spec

> **Repo:** `mcp-gateway`  
> **Status:** Cross-repo ownership and source contracts are recorded in [`docs/workspace-alignment.md`](docs/workspace-alignment.md). ArXiv is the first integration path; evidence normalization, evaluator semantics, explicit backend selection, and shared deployment still have release gates.  
> **Last Updated:** 2026-10-15

> **Evidence note:** This document defines the intended direction. It is not a claim that the four repositories currently deploy or operate as one system. In particular, the gateway's retrieval URL and response shape do not currently match `rag-research`, and this repository has no Compose file for the complete stack.

---

## 1. System Objective

Build a small, dependable **orchestration gateway** for research QA. The gateway accepts a request, chooses a suitable backend, passes the backend's answer and its supporting evidence to the caller, and records an evaluation when evaluation is available.

The gateway coordinates capabilities owned by the other repositories. It should not become a second RAG engine or duplicate their ingestion, retrieval, generation, and evaluation logic.

### Intended user flow

```text
Client
  → Gateway API
  → select one research backend for this request
  → receive answer + evidence + provenance
  → optionally evaluate that exact answer against that exact evidence
  → return the answer, sources, evaluation status, and trace ID
  → persist operational record (without making persistence a dependency for answering)
```

The answer must remain attributable to the backend and evidence that produced it. Combining evidence from multiple backends is a later, explicit feature with its own source and evaluation rules.

---

## 2. Workspace Product Boundaries

| Repository | Product responsibility | Owns | Should not own |
|---|---|---|---|
| [`arxiv-research-assistant`](../arxiv-research-assistant/) | ArXiv paper search and assistant application | ArXiv ingestion, paper metadata, paper-scoped retrieval and answer generation, its user-facing API | Workspace-wide orchestration or the canonical evaluation dataset for every other backend |
| [`rag-research`](../rag-research/) | Research RAG engine and retrieval research | Corpus/chunk pipeline, hybrid retrieval, confidence/abstention, grounded generation, retrieval and pipeline experiments | Calling other repos to complete its core query path |
| [`llm-eval-framework`](../llm-eval-framework/) | Evaluation library and optional evaluation service | Reproducible faithfulness/robustness evaluators, datasets, reports, evaluation API | Being treated as a truth oracle or silently deciding whether an answer is factually correct |
| `mcp-gateway` | Request routing and orchestration boundary | Public API, backend selection, adapter/contract validation, trace propagation, timeouts, partial-failure handling, aggregate operational records | Document ingestion, vector search, answer generation, or evaluator internals |

### Recommended product shape

- Keep `arxiv-research-assistant` as the **ArXiv-focused application**.
- Keep `rag-research` as the **general research RAG engine and retrieval research project**.
- Keep `llm-eval-framework` as the **evaluation toolkit**, with its HTTP service as an optional integration adapter.
- Make `mcp-gateway` the **thin orchestration/API layer** that routes to one backend and provides a consistent response envelope.
- Describe the gateway as an HTTP orchestration gateway until MCP protocol transport and interoperability are actually implemented. A tool registry inside a Go process is useful, but by itself does not establish MCP wire-protocol support.

---

## 3. Canonical Contracts

### 3.1 Backend answer contract

Each answer-producing backend adapter must normalize its response to this internal shape:

```json
{
  "answer": "Answer text",
  "evidence": [
    {
      "text": "Supporting passage",
      "source_id": "paper-or-document-id",
      "chunk_id": "chunk-id",
      "uri": "optional source link"
    }
  ],
  "backend": "rag-research",
  "model": "provider/model",
  "latency_ms": {"retrieval": 0, "generation": 0},
  "decision": "answer",
  "failure_type": "none"
}
```

Fields unavailable from a backend may be omitted or set to `null`; adapters must not fabricate values. Preserve the backend's abstention and failure reason. Preserve evidence identifiers and source links when available.

### 3.2 Evaluation contract

Evaluation receives the **final answer and the exact evidence used to produce it**, plus the original question and trace ID. A score is diagnostic evidence support, not proof of correctness. Evaluation failure must produce `evaluation_status: "unavailable"` (or equivalent) and must not turn a valid backend answer into a fabricated zero score.

Do not evaluate a raw retrieved chunk as though it were a generated answer. Do not compare an answer from one backend against context from another backend unless a future multi-source flow explicitly records that provenance.

### 3.3 External API contract

The gateway response should use stable lowercase JSON keys and include:

- `request_id` / `trace_id`
- `answer`, `evidence`, and `backend`
- `decision` and structured `failure_type`
- `evaluation_status` and an optional evaluation result
- per-stage latency when provided by the backend

Validate requests and tool parameters, cap `top_k` and input size, and return useful HTTP status codes. Keep internal service URLs configurable rather than compiled into adapters.

---

## 4. Current Integration Gaps to Close

These are source-level integration mismatches, not benchmark results:

1. `mcp-gateway/internal/tools/retrieve_chunks.go` calls `POST /retrieve` on port `8003` and expects `results`; this does not match the RAG answer API and is not used by active `/ask` or `/plan` flows.
2. `rag-research` currently serves `POST /generate` on port `8000`; its response has `answer` and `retrieved_chunks`, along with confidence and abstention fields.
3. The gateway has no deployment definition joining the services. ArXiv infra Compose does not start the ArXiv Go/ML apps, RAG Compose has its own network, and evaluator/Ollama wiring is separate.
4. Active `/ask` and `/plan` use backend-scoped plans, but legacy `planner.MakePlan` and `ExecuteWithRetry` remain callable and can mix answer/context if reused.
5. The legacy executor may use retrieved text as the answer; keep it out of public answer flows and remove or constrain that path.
6. The active executor does not normalize ArXiv `sources[]` into public `evidence[]`; response provenance is not yet returned.
7. The evaluator API omits `ground_truth` and evaluator version. Its short-answer code reads missing ground truth; the gateway now skips abstentions and answers shorter than five words, but the evaluator contract still needs correction before those scores can be used.
8. `ARXIV_SERVICE_URL` and `EVAL_SERVICE_URL` are configurable in the gateway; the legacy RAG adapter remains hard-coded. Container deployments still need correct service URLs, a shared network, and a reachable `OLLAMA_HOST`.
9. The gateway request ID is used inside tool execution, but adapters do not yet propagate it as a downstream header.

Resolve these integration gaps before advertising the gateway's closed-loop flow as working end to end.

The actionable, phase-by-phase checklist and completion criteria are maintained in [`IMPLEMENTATION_TODO.md`](IMPLEMENTATION_TODO.md). Treat its checked items as source-level implementation status, not proof that a full deployment has been run.

---

## 5. Roadmap — Phase-Gated Work

### Phase 0 — Declare the boundaries

- Keep the four repository responsibilities in Section 2 as the working product map.
- Decide whether the first gateway release routes to `rag-research`, `arxiv-research-assistant`, or both as separate backend choices.
- Keep multi-backend answer synthesis out of the first release.
- Record source-verified request/response contracts and known deployment assumptions in [`docs/workspace-alignment.md`](docs/workspace-alignment.md).
- Target explicit client backend selection (`arxiv` or `research_rag`) for the coherent multi-backend release; the current ArXiv default is transitional.

**Exit gate:** A maintainer can explain which service owns retrieval, generation, evaluation, and orchestration without ambiguous overlap.

### Phase 1 — Make one query path real

- Select one backend path and implement its adapter against the actual API schema.
- Return answer and its originating evidence together; retain abstention and provenance fields.
- Ensure evaluation uses that exact answer/evidence pair; represent an unavailable evaluator as unavailable.
- Add a small end-to-end contract check using stubbed downstream services; no external model call is required for this contract check.

**Exit gate:** A request through the gateway returns a schema-valid answer or explicit abstention from the selected backend; downstream failures are visible and do not masquerade as a successful score.

### Phase 2 — Make the workspace reproducible

- Provide one documented deployment path (root Compose, deployment repository, or explicit per-repo setup).
- Configure service URLs, Ollama address/model, database DSN, and credentials via environment.
- Add readiness checks for dependencies and verify the gateway reports downstream availability separately from process liveness.
- Document migrations, persistent volumes, startup order, and shutdown behavior.

**Exit gate:** A new developer can start the documented path and verify each service using the documented health endpoints.

### Phase 3 — Reliability and observability

- Add request limits, per-service timeouts, bounded retries, and clear partial-failure behavior.
- Propagate one request/trace ID across gateway and backend logs where supported.
- Record evaluation version, backend/model, decision, evidence IDs, latency, and failure type; avoid logging secrets and consider query privacy.
- Add service-level metrics and dashboards only after metric names and ownership are defined.

**Exit gate:** Failure injection shows bounded response time, a diagnosable failure status, and no false “evaluated successfully” response.

### Phase 4 — Evaluate and expand routing

- Define gateway-specific acceptance cases, including backend selection, abstention propagation, evaluator failure, and provenance.
- Measure end-to-end latency and cost separately from retrieval-only and evaluator-only timings.
- Add a second backend only when it has a distinct user value and a tested adapter.
- Consider MCP protocol support as an explicit feature with protocol-level interoperability tests.

**Exit gate:** Routing changes have repeatable acceptance evidence and preserve answer/evidence provenance.

---

## 6. Success Criteria

| Area | Acceptance condition |
|---|---|
| Contract correctness | Each enabled adapter matches a documented request/response schema and has contract coverage |
| Grounding handoff | Evaluator receives the final answer and the evidence that backend actually used |
| Failure handling | Backend/evaluator/database failures are represented distinctly; no empty or fake score is reported as success |
| Provenance | Response and logs identify backend, model if known, source/chunk IDs if supplied, and trace ID |
| Reproducibility | One documented deployment path configures service discovery and external dependencies |
| Safety | Request/tool inputs have limits; secrets are not stored in source or emitted in logs |
| Claims | README statements about MCP, production readiness, closed-loop evaluation, and performance match verified implementation/evidence |

Do not set numeric latency or quality targets until a representative, versioned gateway workload and measurement procedure are defined.

---

## 7. Non-Goals for the First Release

- Reimplementing retrieval or generation in Go.
- Treating faithfulness similarity as factual truth or calibrated confidence.
- Merging answers and contexts from multiple backends without explicit provenance handling.
- Retrying generation merely because an evaluator score is low; first diagnose whether the score is valid and whether retrieval can change the evidence.
- Claiming production readiness based on local smoke tests or historical measurements.
- Claiming MCP protocol compatibility without implementing and validating that protocol.

