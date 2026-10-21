# ADR-002: Polyglot Architecture — Go untuk Gateway, Python untuk ML Services

- **Status**: Accepted
- **Date**: 2026
- **Author**: Maliki Mayzar

---

## Context

Sistem ini menggabungkan dua domain yang berbeda: infrastruktur (HTTP routing, concurrency, tool orchestration) dan ML/AI (retrieval, evaluation, LLM inference). Keduanya punya ekosistem dan strength yang berbeda di bahasa yang berbeda.

Pertanyaan utama: satu bahasa saja (Go atau Python), atau polyglot?

---

## Decision

Gunakan **Go untuk MCP Gateway** dan **Python untuk semua ML services**, berkomunikasi lewat HTTP internal.

```
Go (Gateway :8090)
  └── HTTP → Python FastAPI (arxiv :8001)
  └── HTTP → Python FastAPI (eval  :8002)
  └── HTTP → Python FastAPI (rag   :8000)
```

---

## Consequences

### Kenapa Go untuk Gateway

- **Concurrency model** — goroutines dan channels ideal untuk handle multiple concurrent tool calls dengan timeout control via `context.WithTimeout`
- **Performance** — low latency untuk routing dan orchestration layer, tidak ada GIL
- **Type safety** — interface design yang strict untuk tool registry mencegah runtime error
- **Binary deployment** — single binary, tidak ada dependency hell di production
- **Chi router** — lightweight, idiomatic, middleware composable

### Kenapa Python untuk ML Services

- **Ekosistem ML** — FAISS, sentence-transformers, rank-bm25, semua native Python
- **Existing codebase** — ketiga repo pendukung sudah Python, rewrite = buang waktu
- **FastAPI** — async, auto schema generation, minimal boilerplate untuk wrap existing logic
- **Ablation study results** — hasil benchmark BM25+Hybrid sudah ada di Python, tidak perlu port

### Tradeoff

- **Operational complexity** — 4 proses berbeda (1 Go + 3 Python) vs 1 monolith
- **Network hop** — setiap tool call ada latency tambahan ~1-5ms untuk HTTP internal
- **Mitigasi**: Docker Compose mengelola semua service, network internal Docker sangat cepat

### Alternatif yang Ditolak

**Full Go**: Ekosistem ML di Go tidak mature. Port FAISS dan BM25 ke Go tidak worth it.

**Full Python**: Gateway di Python bisa, tapi concurrency model-nya lebih kompleks untuk handle parallel tool calls dengan proper timeout. GIL jadi bottleneck.

**gRPC antar service**: Overkill untuk internal communication. HTTP + JSON cukup untuk throughput yang dibutuhkan.

---

## Referensi
- `cmd/server/main.go` — Go gateway entry point
- `internal/registry/registry.go` — tool registry dengan interface design
- Repo `rag-research` — Python FastAPI wrapper untuk retrieval
- Repo `llm-eval-framework` — Python FastAPI wrapper untuk evaluation