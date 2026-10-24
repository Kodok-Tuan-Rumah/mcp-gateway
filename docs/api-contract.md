# Gateway API Contract (v1)

> **Normatif.** Go types di `internal/api/contract.go` harus cocok dengan dokumen ini.
> Contoh JSON di §3 dihasilkan dari Go types sungguhan dan dikunci golden-file test.
>
> **Status (2026-10-23):**
> - **§2 Routing** — keputusan final, sudah disepakati.
> - **§1 Request dan §3 Response** — tipe Go ada di `internal/api/contract.go`
>   dengan test kontrak dan golden file, dan **sudah terpasang** di `/ask` dan
>   `/plan` (`cmd/server/router.go`).
> - Adapter ArXiv dan RAG meneruskan evidence terstruktur dari backend terpilih
>   ke `GatewayResponse`; evaluator menerima context dari evidence yang sama.
> - `backend` bersifat opsional dan default ke `arxiv`; client dapat memilih
>   `arxiv` atau `research_rag` secara eksplisit.
> - `/ask` dan `/plan` mengeksekusi plan deterministik yang sama. `/plan` tetap
>   tersedia sebagai alias kompatibilitas, bukan endpoint preview.
> - Evaluasi dilewati untuk abstensi dan evidence/context kosong. Detail lintas-repo ada di
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
| `backend` | enum | tidak | `"arxiv"` | `"arxiv"` atau `"research_rag"` |
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
- Backend yang dipilih menentukan satu adapter jawaban dan langkah evaluasi.
  Tidak ada planner eksternal atau pemilihan tool non-deterministik.
- `/ask` dan `/plan` menjalankan flow yang sama dan mengembalikan response
  contract yang sama. `/plan` dipertahankan sebagai alias kompatibilitas.
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

Adapter `retrieve_chunks` sudah dihapus karena `rag-research` tidak menyediakan
endpoint `/retrieve`. Gunakan backend `research_rag`, yang memanggil `/generate`.

### Alasan

| Keputusan | Alasan |
|---|---|
| Client memilih backend | Deterministik; client yang tahu apakah kebutuhannya corpus ArXiv atau dokumen umum |
| Default `arxiv` | Menjaga kompatibilitas request lama; client dapat memilih `research_rag` secara eksplisit |
| `research_rag` via `/generate` | `rag-research` menjawab di `POST /generate` (port 8000) dengan `answer` + `retrieved_chunks` |
| Deterministic plan | Satu adapter jawaban per backend, lalu evaluasi jika ada jawaban dan evidence |

> Client sebaiknya mengirim `backend` secara eksplisit agar maksud pemilihan
> ArXiv atau corpus riset umum terlihat pada request.

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
  "orchestrator": "rule-based"
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

### 3.3 Abstensi RAG karena klaim lemah

`research_rag` mempertahankan kategori `unsupported_claims` agar konsumen dapat
membedakannya dari konteks yang memang tidak cukup. Metadata refleksi hanya
ada bila pemeriksaan dijalankan:

```json
{
  "request_id": "req-abc127",
  "answer": "INSUFFICIENT_CONTEXT",
  "backend": "research_rag",
  "decision": "abstain",
  "failure_type": "unsupported_claims",
  "reflection_passed": false,
  "reflection_score": 0.5,
  "unsupported_claims_count": 2,
  "evaluation_status": "skipped",
  "query": "apa hasil studi tersebut?"
}
```

`reflection_score` adalah fraksi kalimat yang melewati ambang lexical coverage;
`unsupported_claims_count` menghitung kalimat yang berada di bawah ambang, bukan
klaim atomik yang diekstrak. Ini heuristik eksperimental, bukan bukti kebenaran
atau confidence yang terkalibrasi. Jika refleksi tidak dijalankan, ketiga field
opsional tersebut dihilangkan.

### 3.4 Kegagalan provider `research_rag` — error, bukan abstensi

Ketika `research_rag` gagal secara operasional (provider LLM tidak tersedia:
429 rate limit, koneksi gagal, auth invalid, JSON response invalid), backend
melaporkan `failure_type: "provider_error"` beserta `provider_error_kind`.
Gateway memetakan ini ke **`decision: "error"` + HTTP 502** — bukan 200 abstain,
karena menyembunyikan kegagalan operasional sebagai abstain kontekstual
`INSUFFICIENT_CONTEXT` menyesatkan konsumen yang mengukur kualitas grounding.
Jawaban sentinel dari backend dihapus; evaluasi tidak dijalankan.

```json
{
  "request_id": "req-abc129",
  "answer": "",
  "backend": "research_rag",
  "decision": "error",
  "failure_type": "provider_error",
  "provider_error_kind": "rate_limit",
  "evaluation_status": "skipped",
  "query": "apa itu RAG?"
}
```

`provider_error_kind` mengikuti taksonomi backend (`rate_limit`, `connection`,
`auth`, `invalid_json`, `not_configured`, `provider`) dan hanya ada bila
`failure_type` = `provider_error`. Kategori kegagalan generation lain
(`sanity_check`, `empty_output`) juga dilaporkan sebagai `decision: "error"`
dengan alasan yang sama: itu kegagalan produksi jawaban, bukan keputusan
abstensi yang sah.

### 3.5 Evaluator tidak tersedia — kunci `"score": 0`

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

### 3.6 Error validasi / backend tidak tersedia

```json
{
  "request_id": "req-abc126",
  "failure_type": "invalid_request",
  "message": "backend nonsense is not recognized; use `arxiv` or `research_rag`",
  "field": "backend"
}
```

### 3.7 HTTP status

| Status | Kapan |
|---|---|
| `200` | `decision` = `answer` atau `abstain` (termasuk saat evaluator `unavailable`) |
| `400` | Request tidak valid, atau `backend` tidak dikenal/tidak tersedia |
| `502` | Backend menjawab gagal — `decision` = `error` (termasuk `failure_type` = `provider_error`, `sanity_check`, `empty_output` dari backend) |

Kegagalan evaluator **tidak** mengubah status HTTP: jawaban backend tetap
berharga dan dikembalikan dengan 200.

Acceptance checks menggunakan HTTP stub untuk memverifikasi adapter ArXiv,
status malformed/non-2xx/timeout, abstensi, serta jalur `/ask` dan `/plan`
sampai response publik. Coverage pengujian ada di
`internal/tools/search_arxiv_test.go` dan `cmd/server/acceptance_test.go`.

### 3.8 Aturan field

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
| `reflection_passed`, `reflection_score`, `unsupported_claims_count` | tidak | Hanya dari backend `research_rag` bila refleksi dijalankan; metrik lexical eksperimental |
| `provider_error_kind` | tidak | Hanya bila `failure_type` = `provider_error`; dipetakan apa adanya dari `research_rag` |

`evidence` dan `answer` berada dalam satu struct (`BackendAnswer`) supaya
keduanya tidak bisa berasal dari backend berbeda.

---

## 4. Yang SENGAJA belum ada di v1

| Tidak ada | Alasan |
|---|---|
| Adapter `retrieve_chunks` | `rag-research` tidak punya `/retrieve`; `research_rag` menjawab via `/generate` |
| Auto-retry | Tidak aktif pada jalur jawaban; kebijakan retry masih perlu keputusan operasional |
| Readiness check | `/health` adalah liveness; `/ready` melaporkan kapabilitas ArXiv/RAG dan mengembalikan 200 jika setidaknya satu backend jawaban tersedia. Evaluator bersifat opsional untuk menjawab |
| `X-Request-ID` ke downstream | Belum dipropagasikan oleh adapter; termasuk gate observability |
