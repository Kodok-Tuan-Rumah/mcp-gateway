# ADR-003: Dual Orchestration — Groq LLM + Rule-based Fallback

> **Superseded (2026-10-23):** `/ask` and `/plan` now use one deterministic
> backend plan. The Groq planner and its gateway dependency have been removed.

- **Status**: Accepted
- **Date**: 2026
- **Author**: Maliki Mayzar

---

## Context

Sistem membutuhkan cara untuk memutuskan tool mana yang dipanggil dan dalam urutan apa berdasarkan user query. Ada spektrum pendekatan: fully rule-based (deterministik) sampai fully LLM-driven (fleksibel tapi tidak reliable).

Tantangan utama: LLM planning bisa gagal (API timeout, malformed JSON response, rate limit). Sistem tidak boleh down hanya karena LLM tidak bisa dihubungi.

---

## Decision

Implementasi **dual orchestration** dengan Groq sebagai primary planner dan rule-based sebagai fallback:

```
POST /ask
  │
  ├─► Groq LLM (llama-3.3-70b) ──► parse JSON plan ──► Execute
  │         │
  │    [jika gagal]
  │         │
  └─► Rule-based Planner ──► Execute
```

Groq dipilih karena:
- OpenAI-compatible API, mudah di-swap
- llama-3.3-70b: open model, performa tinggi untuk structured output
- Latency lebih rendah dari OpenAI untuk inference tasks

---

## Consequences

### Keuntungan

- **Resilience** — sistem tetap jalan meski Groq API down atau rate limited
- **Graceful degradation** — user tidak tahu ada fallback, response tetap valid
- **Observability** — field `orchestrator` di response menunjukkan `"groq"` atau `"rule-based"`, sehingga bisa dimonitor di Grafana berapa % request yang fallback
- **Development velocity** — rule-based planner bisa ditest tanpa butuh API key

### Tradeoff

- **Dua code path** — maintenance overhead untuk keep keduanya in sync
- **Inconsistency** — Groq plan bisa lebih fleksibel dari rule-based, hasil bisa berbeda untuk query yang sama
- **Mitigasi**: Evaluation loop otomatis score setiap response — perbedaan kualitas terukur

### Kenapa Tidak Pure Rule-based

Rule-based tidak bisa handle query ambigu atau multi-step yang tidak masuk pattern. Contoh: "compare attention mechanisms in transformers vs mamba" butuh reasoning untuk decide tool sequence yang optimal.

### Kenapa Tidak Pure LLM

Reliability bergantung pada eksternal service. Untuk production system, single point of failure di LLM API tidak acceptable.

### JSON Parsing Strategy

Groq response di-strip dari markdown fences sebelum di-parse:
```go
func extractJSON(raw string) string {
    start := strings.Index(raw, "{")
    end   := strings.LastIndex(raw, "}")
    return raw[start : end+1]
}
```
Ini handle kasus LLM wrap response dalam ```json ... ``` block.

---

## Referensi
- `internal/orchestrator/groq.go` — Groq LLM planner
- `internal/planner/planner.go` — rule-based planner
- `cmd/server/main.go` — fallback logic di `/ask` handler
