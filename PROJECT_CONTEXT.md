# PROJECT: AI Research Workspace Gateway

> **Project direction:** Keep the gateway thin. It routes research questions to a clearly owned backend and returns the answer with its evidence, provenance, and honest evaluation status. The four repositories remain independently useful services/projects; this workspace is not yet a verified single deployment.
> **Source snapshot:** 2026-10-15. `PROJECT_GOALS.md` defines intended boundaries and phase gates; this file summarizes current source-level architecture and known integration state.

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
  → validate request and restrict tools to the selected backend (currently ArXiv only)
  → call ArXiv adapter; /ask may use a filtered Groq plan, /plan uses a deterministic plan
  → executor extracts answer/source context and optionally calls evaluator
  → map result to the public response; persist only a valid evaluation score asynchronously
```

The code has the beginnings of this flow, but it is not yet an end-to-end working integration:

* Gateway `retrieve_chunks` still points to `http://arxiv-rag-service:8003/retrieve`; `rag-research` currently exposes `/generate` on port 8000 and returns `retrieved_chunks`, not `results`.
* The ArXiv adapter calls the Go backend `/query`, which already performs retrieval and generation. The local RAG adapter is intended to call a different service; their answers and contexts must not be mixed accidentally.
* The active `/ask` and `/plan` routes use backend-scoped plans and do not include `retrieve_chunks`. The old `MakePlan` and `ExecuteWithRetry` helpers still exist as a legacy path; they can mix answers and contexts if called, so they should be removed or isolated.
* The active response does not yet normalize ArXiv `sources[]` into structured `evidence[]`; source text is currently extracted for evaluator context only.
* The legacy executor may use retrieved context text itself as `answer`. That is not a meaningful final answer to score and must remain outside public answer flows.
* Groq plans are now constrained to one ArXiv answer step and an optional evaluation step placed afterward; query and `top_k` are taken from the validated client request.
* ArXiv and evaluator base URLs are configurable with `ARXIV_SERVICE_URL` and `EVAL_SERVICE_URL`. The legacy retrieval adapter remains hard-coded and unsupported; a shared deployment still needs explicit service/network configuration.
* The evaluator uses Ollama embeddings. Its default host is localhost, which is generally not the Ollama service when the evaluator runs in its own container. Its short-answer path also requires `ground_truth` that the HTTP API does not accept; the gateway skips abstentions and answers shorter than five words until that contract is fixed.
* There is no gateway Compose file. Existing Compose files are per repository and do not configure one shared service network.

These observations are integration status only; no deployment or runtime behavior was verified during this source review.

## RECOMMENDED FIRST RELEASE

1. Complete ArXiv as the first gateway vertical slice because its current `/query` response already returns an answer and source passages; add `rag-research` as a separate explicit backend for general research documents.
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

