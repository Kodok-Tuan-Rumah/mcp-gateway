# Gateway API Contract (v1)

> **Normatif.** Go types di `internal/api/contract.go` harus cocok dengan dokumen ini.
> Contoh JSON di §3 dihasilkan dari Go types sungguhan dan dikunci golden-file test.
>
> **Status (2026-10-15):**
> - **§2 Routing** — keputusan final, sudah disepakati.
> - **§1 Request dan §3 Response** — tipe Go ada di `internal/api/contract.go`
>   dengan test kontrak dan golden file, dan **sudah terpasang** di `/ask` dan
>   `/plan` (`cmd/server/router.go`).
> - Evidence terstruktur kini diteruskan dari response ArXiv melalui
>   `registry.ToolResponse.TypedResult` dan `planner.ExecutionResult` ke
>   `GatewayResponse`. Context evaluator dibentuk dari teks evidence yang sama;
>   sumber, score, model, dan latency tidak direkonstruksi dari context gabungan.
> - Gateway menandai pemilihan backend sebagai opsional dan default ke `arxiv`
>   untuk jalur awal. Arah workspace untuk rilis dua-backend adalah pemilihan
>   eksplisit; default ini bersifat transisi.
> - Evaluasi dilewati untuk abstensi ArXiv dan evidence/context kosong. Jawaban
>   pendek tetap dievaluasi secara konservatif dengan exact match terhadap
>   context evidence oleh evaluator. Detail lintas-repo ada di
>   [`workspace-alignment.md`](workspace-alignment.md).
>
> Kontrak per adapter: [`docs/tool-contracts/`](tool-contracts/).

---

## 1. Request

`POST /ask`, `POST /plan`

```json
{
  "backend": "arxiv",
  "query": "apa itu RAG?",
  "top_k": 5
}
```

| Field | Tipe | Required | Default | Batas |
|---|---|---|---|---|
| `backend` | enum | tidak | `"arxiv"` | hanya `"arxiv"` di v1 |
| `query` | string | ya | — | 3..1000 karakter |
| `top_k` | int | tidak | `5` | 1..20 |

> Batas `query` dan `top_k` mengikuti `rag-research` (`GenerateRequest`, min 3 /
> max 1000; `top_k` 1..20) supaya gateway tidak meneruskan parameter yang akan
> ditolak downstream.
>
> Field tak dikenal **ditolak** dengan 400 (`DisallowUnknownFields`), dan body
> dibatasi 1 MiB (`http.MaxBytesReader`).

---

## 2. Routing (keputusan v1)

- Satu request = **satu** backend. Tidak ada jawaban yang digabung lintas backend di v1.
- `backend` yang tidak dikenal atau belum tersedia → **HTTP 400** dengan
  `failure_type: "backend_not_available"`. Bukan diam-diam fallback.
- **Groq tidak memilih backend.** Groq hanya menyusun langkah di dalam backend
  terpilih; langkah dengan tool milik backend lain dibuang. Backend ditentukan
  oleh client, supaya routing deterministik.
- `/tool` adalah passthrough langsung ke registry, dengan bentuk JSON sendiri
  (`tool_name`, `trace_id`, `success`, `data`, `error`). **Bukan** bagian dari
  kontrak jawaban — jangan dipakai untuk integrasi.
  Bentuk ini dikunci golden-file di `internal/registry/testdata/*.json`.
  Key-nya snake_case; bentuk PascalCase ditolak (Go tidak menjembatani
  `ToolName` ke `tool_name`).

### Peta tool per backend

| Backend | Tool yang boleh dipakai | Status |
|---|---|---|
| `arxiv` | `search_arxiv`, `evaluate_answer` | ✅ aktif |
| `research_rag` | `research_rag`, `evaluate_answer` | ✅ aktif (adapter `/generate`) |

`retrieve_chunks` **tidak ada di backend mana pun** pada v1: adapter-nya masih
menembak endpoint yang tidak ada di `rag-research`. Lihat
[`tool-contracts/retrieve_chunks.json`](tool-contracts/retrieve_chunks.json).

### Alasan

| Keputusan | Alasan |
|---|---|
| Client memilih backend | Deterministik; client yang tahu apakah kebutuhannya corpus ArXiv atau dokumen umum |
| Default `arxiv` | Satu-satunya adapter yang kontraknya sudah terverifikasi terhadap source (`/query` → `answer` + `sources[{text, score, arxiv_id}]`) |
| `research_rag` via `/generate` | `rag-research` menjawab di `POST /generate` (port 8000) dengan `answer` + `retrieved_chunks`; adapter-nya terpisah dari `retrieve_chunks` yang tetap tidak dipakai |
| Groq tidak memilih backend | Memilih tool secara LLM bersifat non-deterministic, sedangkan routing backend ditentukan client; Groq hanya menyusun urutan langkah |

> **Target kontrak workspace:** setelah adapter RAG aktif, client mengirim
> `backend` secara eksplisit. Default ArXiv di atas hanya untuk tahap integrasi
> satu backend dan akan ditinjau sebelum rilis dua-backend.

---

## 3. Response

Envelope kanonik adalah `GatewayResponse` di `internal/api/contract.go`.
`BackendAnswer` di-embed, jadi field-nya rata di JSON.

Contoh di bawah ini **dihasilkan langsung dari Go types** dan dikunci oleh
golden-file test (`internal/api/testdata/*.json`). Kalau bentuk JSON berubah,
`go test ./internal/api/` gagal.

### 3.1 Jawaban normal

```json
{
  "request_id": "req-abc123",
  "answer": "RAG menggabungkan retrieval dengan generation.",
  "evidence": [
    {"text": "Retrieval augmented generation...", "source_id": "2101.00001", "score": 0.82},
    {"text": "Chunk kedua...", "chunk_id": "chunk-7"}
  ],
  "backend": "arxiv",
  "model": "phi3:mini",
  "latency_ms": {"retrieval": 120, "generation": 3400, "total": 3520},
  "decision": "answer",
  "failure_type": "none",
  "evaluation_status": "completed",
  "evaluation": {"score": 0.86, "failure_mode": "none", "supported_claims": 3, "total_claims": 3, "version": "1.2.1"},
  "query": "apa itu RAG?",
  "orchestrator": "groq"
}
```

> `evidence[]`, `model`, dan `latency_ms` diisi dari response backend ArXiv;
> field metadata yang tidak dilaporkan backend tetap absen.

### 3.2 Abstensi backend (dipertahankan, bukan diubah jadi jawaban)

```json
{
  "request_id": "req-abc124",
  "answer": "Not found in context.",
  "backend": "arxiv",
  "decision": "abstain",
  "failure_type": "insufficient_context",
  "evaluation_status": "skipped",
  "query": "hasil GPT-5 terbaru?"
}
```

### 3.3 Evaluator tidak tersedia — kunci `"score": 0`

```json
{
  "request_id": "req-abc125",
  "answer": "Jawaban tetap valid.",
  "backend": "arxiv",
  "decision": "answer",
  "failure_type": "none",
  "evaluation_status": "unavailable",
  "query": "apa itu BM25?"
}
```

**Perhatikan: tidak ada key `score` sama sekali.** Jawaban backend tetap
dikembalikan, `decision` tetap `answer`, `failure_type` tetap `none`, dan
ketiadaan skor dinyatakan lewat `evaluation_status` — bukan lewat angka 0.
`Score` bertipe `*float64` dengan `omitempty` supaya "belum dinilai" (nil)
secara struktural mustahil diserialisasi menjadi 0, dan
`TestScore_NilIsAbsentNotZero` menjaganya.

### 3.4 Error validasi / backend tidak tersedia

```json
{
  "request_id": "req-abc126",
  "failure_type": "invalid_request",
  "message": "backend nonsense is not recognized; use `arxiv` or `research_rag`",
  "field": "backend"
}
```

### 3.5 HTTP status

| Status | Kapan |
|---|---|
| `200` | `decision` = `answer` atau `abstain` (termasuk saat evaluator `unavailable`) |
| `400` | Request tidak valid, atau `backend` tidak dikenal/tidak tersedia |
| `502` | Backend menjawab gagal — `decision` = `error` |

Kegagalan evaluator **tidak** mengubah status HTTP: jawaban backend tetap
berharga dan dikembalikan dengan 200.

Acceptance checks menggunakan HTTP stub untuk memverifikasi adapter ArXiv,
status malformed/non-2xx/timeout, abstensi, serta jalur `/ask` dan `/plan`
sampai response publik. Coverage pengujian ada di
`internal/tools/search_arxiv_test.go` dan `cmd/server/acceptance_test.go`.

### 3.6 Aturan field

| Field | Wajib? | Catatan |
|---|---|---|
| `request_id` | ya | Satu ID per request, diteruskan ke log |
| `decision` | ya | `answer` / `abstain` / `error` |
| `failure_type` | ya | `"none"` kalau tidak ada kegagalan |
| `evaluation_status` | ya | `completed` / `unavailable` / `skipped` |
| `answer` | ya | Boleh kosong kalau `decision` bukan `answer` |
| `backend` | ya | Backend yang benar-benar menjawab |
| `evidence[]` | tidak | Bukti terstruktur dari backend yang sama dengan `answer` |
| `model` | tidak | Hanya kalau backend melaporkannya |
| `latency_ms` | tidak | Hanya kalau backend melaporkannya |
| `evaluation.score` | tidak | **Hanya kalau benar-benar dinilai** |

`evidence` dan `answer` berada dalam satu struct (`BackendAnswer`) supaya
keduanya tidak bisa berasal dari backend berbeda.

---

## 4. Yang SENGAJA belum ada di v1

| Tidak ada | Alasan |
|---|---|
| `retrieve_chunks` dari jalur jawaban | `rag-research` tidak punya `/retrieve`; `research_rag` menjawab via `/generate` |
| Auto-retry | Sudah dihapus dari jalur jawaban; masih memakai trigger `score` yang belum valid (Phase 2) |
| Readiness check | `/health` hanya liveness (Phase 4) |
| `X-Request-ID` ke downstream | Adapter belum meneruskan header (Phase 3) |
