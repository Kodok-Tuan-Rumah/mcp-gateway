# PROJECT: AI Research Workspace Gateway

> **Project direction:** Keep the gateway thin. It routes research questions to a clearly owned backend and returns the answer with its evidence, provenance, and honest evaluation status. The four repositories remain independently useful services/projects; this workspace is not yet a verified single deployment.
> **Source snapshot:** 2026-10-23. `PROJECT_GOALS.md` defines intended boundaries and phase gates; this file summarizes current source-level architecture and known integration state.

## GOAL

Build a reliable orchestration entry point for the research tools in this workspace:

* choose and call an appropriate answer backend
* preserve the backend's answer, evidence, abstention, and source identity
* optionally evaluate that exact answer against its exact supporting context
* expose traceable status and stage latency without hiding downstream failure

The gateway coordinates the services; it does not own their retrieval, generation, ingestion, or evaluation algorithms.

## FOUR-REPOSITORY PRODUCT MAP

```text
Client
  → mcp-gateway (HTTP entry point, routing, adapters, trace, failure handling)
      ├─ arxiv-research-assistant (ArXiv ingestion + ArXiv-focused QA)
      ├─ rag-research (research RAG retrieval, confidence/abstention + generation)
      └─ llm-eval-framework (evaluation library and optional evaluation API)
```

Suggested boundaries:

* **`arxiv-research-assistant`:** own the ArXiv paper lifecycle and its application behavior: fetch/ingest papers, store metadata/vectors, answer paper-oriented queries, and return source passages.
* **`rag-research`:** own the general research corpus QA engine, hybrid retrieval experiments, confidence/abstention behavior, and its own quality evidence.
* **`llm-eval-framework`:** own reusable evaluators, carefully versioned evaluation datasets, offline reports, and an optional HTTP wrapper. Its faithfulness score measures evidence support under its method; it is not a correctness oracle.
* **`mcp-gateway`:** own request routing, stable cross-service response shape, downstream configuration, timeouts, request IDs, and operational aggregation. Do not make the gateway a third RAG implementation.

## CURRENT SOURCE-LEVEL FLOW

```text
POST /ask or /plan
  → validate request and select one backend (arxiv or research_rag)
  → execute its deterministic answer step followed by evaluation when applicable
  → executor normalizes answer + evidence and optionally calls evaluator
  → map result to the public response; persist only a valid evaluation asynchronously
  → GET /ready probes ArXiv, evaluator, and RAG concurrently
```

The active answer path is wired end to end at source level:

* The ArXiv adapter calls the Go backend `/query`; the RAG adapter calls `rag-research` `POST /generate`. Both normalize their own answer + evidence, so evidence cannot cross backends.
* `/ask` and `/plan` accept `backend: "arxiv"` or `backend: "research_rag"`; both routes execute the same deterministic plan and response contract. `backend` defaults to `arxiv` for compatibility, though clients should choose explicitly.
* ArXiv `sources[]` and RAG `retrieved_chunks[]` are normalized into structured `evidence[]` by their typed adapters. The evaluator receives the same answer and evidence.
* Evaluation result maps `evaluation_status`, score, failure mode, evaluator version, `supported_claims`, and `total_claims`. An evaluator outage remains `unavailable`; it is never converted to score 0.
* The unsupported `retrieve_chunks` adapter has been removed because `rag-research` does not expose `/retrieve`. The active `research_rag` adapter calls `POST /generate`.
* All three downstream base URLs and timeouts are configurable. `/ready` reports ArXiv and RAG answer capabilities separately and returns 200 if either answer backend is reachable; evaluator failure is reported as degraded but does not block answers. The evaluator probe currently checks only its `/health` endpoint, not embedding readiness.
* The workspace root compose supplies `ARXIV_SERVICE_URL`, `EVAL_SERVICE_URL`, `RAG_SERVICE_URL`, and `OLLAMA_HOST`; this repository itself still has no Compose file.

Runtime verification (containers, monitoring targets, and smoke test) is tracked separately from these source-level statements.

## RECOMMENDED FIRST RELEASE

1. Keep ArXiv and `research_rag` as separate, explicitly selected backends. Source-level adapters exist for both; root Compose/runtime acceptance remains a separate verification gate.
2. Normalize backend results to `answer + evidence[] + backend + decision + failure_type + model/latency when available`.
3. Pass the same answer and evidence into the evaluator; include source/chunk identifiers and evaluator version when available.
4. Return the backend answer even if optional evaluation or persistence is unavailable, but make those states explicit.
5. Provide one reproducible deployment setup and configure service URLs and `OLLAMA_HOST` rather than baking deployment addresses into code.
6. Call this an HTTP orchestration gateway until MCP protocol interoperability is implemented and checked.

## RESPONSE AND FAILURE PRINCIPLES

* A backend abstention stays an abstention; the gateway does not turn it into an answer.
* An evaluator outage means `evaluation_status: unavailable`, not a score of zero and not a failed answer.
* A retrieval-only result is evidence, not a generated response.
* A backend's evidence stays paired with that backend's answer.
* Liveness (`gateway process is running`) and readiness (`required downstreams are reachable`) are different states.
* Retries are bounded and only useful when they change a meaningful stage/input. Do not retry on an invalid or mismatched evaluation score.

## DECISIONS TO KEEP VISIBLE

* Which backend is the default for a general research question?
* Is evaluation synchronous for the first release, or recorded asynchronously as a diagnostic?
* Which service owns the shared deployment definition and secrets configuration?
* Is the external interface ordinary HTTP, MCP, or both? Keep README language aligned with the implemented interface.
* Which logs may retain query text, answer text, and evidence? Set retention and redaction expectations before broad deployment.

See [`PROJECT_GOALS.md`](PROJECT_GOALS.md) for component contracts, integration gaps, phase exit gates, and non-goals.

