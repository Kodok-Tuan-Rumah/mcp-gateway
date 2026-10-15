# MCP Gateway — Task List & Roadmap

> **Tujuan repo:** membangun gateway HTTP yang menerima pertanyaan, memilih satu backend, mempertahankan jawaban beserta buktinya, dan melaporkan hasil evaluasi secara jujur.
> **Status:** roadmap kerja. Checklist hanya ditandai selesai setelah perubahan dibuat dan kriteria penerimaannya diverifikasi.
> **Terakhir ditinjau:** 2026-10-15

Dokumen ini memecah pekerjaan menjadi fase berurutan. Hasil pencocokan kontrak lintas-repo ada di [`docs/workspace-alignment.md`](docs/workspace-alignment.md). Kontrak API ada di [`docs/api-contract.md`](docs/api-contract.md), gambaran arsitektur ada di [`PROJECT_GOALS.md`](PROJECT_GOALS.md), dan kontrak adapter ada di [`docs/tool-contracts/`](docs/tool-contracts/).

Checklist kerja untuk hari ini: [`TODO_HARI_INI.md`](TODO_HARI_INI.md).

## Tujuan akhir

- Satu request menggunakan satu backend jawaban yang dipilih secara eksplisit.
- Jawaban, evidence, dan asal sumber selalu berpasangan dari backend yang sama.
- Kegagalan backend, evaluator, dan penyimpanan dapat dibedakan; skor yang tidak tersedia tidak pernah dianggap nol.
- URL layanan, timeout, dan dependensi operasional dapat dikonfigurasi.
- Dokumentasi menggambarkan kemampuan yang benar-benar tersedia.

## Fase 0 — Kontrak dan batas sistem

**Goal:** kontrak publik, routing v1, dan tanggung jawab masing-masing repo tidak ambigu.

- [x] Definisikan request dan response `/ask` serta `/plan` di `internal/api/contract.go` dan `docs/api-contract.md`.
- [x] Tetapkan default `arxiv`; tolak backend yang belum tersedia secara eksplisit.
- [x] Batasi `/ask` dan `/plan` ke tool backend terpilih melalui `cmd/server/plan.go`.
- [x] Tetapkan kontrak JSON `/tool` dan kontrak adapter di `docs/tool-contracts/`.
- [x] Batasi plan Groq ke satu langkah jawaban ArXiv, ambil query/top_k dari request tervalidasi, dan tempatkan evaluasi setelah jawaban.

**Selesai bila:** kode, dokumen kontrak, dan contoh JSON sepakat; satu request tidak dapat mencampur jawaban atau konteks lintas backend.

## Fase 1 — Selesaikan jalur ArXiv end-to-end

**Goal:** `/ask` dan `/plan` menghasilkan jawaban ArXiv beserta sumber yang benar, dan evaluator menerima konteks yang sama.

- [x] Buat URL ArXiv dan evaluator dapat dikonfigurasi melalui `ARXIV_SERVICE_URL` dan `EVAL_SERVICE_URL`, dengan default lokal yang terdokumentasi.
- [x] Batasi plan Groq ke langkah ArXiv yang valid, ambil query/top_k dari request tervalidasi, dan letakkan evaluasi setelah pencarian.
- [x] Lewati evaluasi untuk abstensi dan context kosong; evaluasi jawaban pendek secara konservatif terhadap evidence.

### 1A. Kunci kontrak ArXiv yang dipakai gateway

- [x] Catat tipe request dan response aktual dari `arxiv-research-assistant/go-backend/internal/api/query.go` sebagai sumber adapter.
- [x] Tetapkan pemetaan: `sources[].text → evidence[].text`, `arxiv_id → source_id`, `score → evidence[].score`.
- [x] Tetapkan pemetaan waktu: `retrieval_ms → latency_ms.retrieval`, `generation_ms → latency_ms.generation`; jangan membuat `total` karena backend tidak melaporkannya.
- [x] Terima `model` dari backend apa adanya; jangan mengisi nilai tebakan bila kosong.
- [x] Catat bahwa abstensi backend saat ini adalah sentinel `Not found in context.` dan belum berupa field status; sentinel dipusatkan di `internal/api`.
- [x] Tambahkan tipe wire request/response/source dan pemetaan `Evidence`, latency, serta tool data di `internal/tools/arxiv_contract.go`.

**Hasil bagian ini:** kontrak tool dan response gateway punya pemetaan field yang lengkap dan tanpa metadata rekaan.

### 1B. Buat adapter ArXiv bertipe dan tahan terhadap respons rusak

- [x] Tambahkan tipe Go untuk request `/query`, response, dan source; adapter kini decode response ke tipe tersebut.
- [x] Periksa error `json.Marshal`/baca body, batasi ukuran response downstream hingga 4 MiB, dan tutup body pada semua jalur.
- [x] Pastikan status non-2xx, JSON malformed atau kehilangan field wajib, timeout, dan koneksi gagal menjadi error tool yang dapat diklasifikasikan gateway.
- [x] Validasi `query` dan `top_k` sebelum request; `model` boleh tidak dikirim karena backend memiliki default `phi3:mini`.

**Hasil bagian ini:** adapter hanya mengembalikan sukses jika response sesuai schema `/query` dan dapat dinormalisasi.

### 1C. Bawa jawaban dan evidence sebagai satu hasil

- [x] Tambahkan representasi hasil ArXiv bertipe di jalur internal; jangan jadikan `map[string]interface{}` dan string gabungan sebagai satu-satunya bentuk data.
- [x] Ubah executor agar mempertahankan daftar evidence beserta ID sumber dan score.
- [x] Bangun context evaluator dari teks evidence yang sama, tanpa mengganti atau mencampur sumber.
- [x] Isi `GatewayResponse.evidence`, `model`, dan latency dari hasil backend.
- [x] Pastikan response tetap memuat jawaban asli ketika evaluator tidak tersedia.

**Hasil bagian ini:** jawaban, evidence, backend, dan metadata turunannya berasal dari satu response ArXiv.

### 1D. Kunci keputusan, abstensi, dan evaluasi

- [x] Pastikan sentinel abstensi tidak berubah menjadi error atau jawaban normal.
- [x] Lewati evaluator untuk abstensi dan konteks kosong; nilai jawaban pendek dengan exact match terhadap evidence.
- [x] Petakan timeout, status non-2xx, dan JSON rusak ke `failure_type` yang sesuai; jangan tampilkan backend failure sebagai abstensi.
- [x] Verifikasi bahwa evaluasi memakai pertanyaan asli, jawaban final, dan context dari evidence yang dikembalikan.
- [x] Simpan hanya skor evaluator yang benar-benar ada dan valid dalam rentang `0..1`; skor `0` tetap valid bila evaluator sungguh mengembalikannya.

**Hasil bagian ini:** answer status dan evaluation status tetap terpisah, dan tidak ada skor hasil tebakan.

### 1E. Contract checks dan penerimaan jalur

- [x] Tambahkan adapter contract checks dengan downstream HTTP stub untuk sukses, parameter invalid, abstain, malformed JSON, timeout, batas ukuran, dan non-2xx.
- [x] Tambahkan pemeriksaan alur router → adapter → executor → response untuk `/ask` dan `/plan`.
- [x] Pastikan pemeriksaan membuktikan evidence dan evaluator context berasal dari response backend yang sama.
- [x] Jalankan pemeriksaan Go: `GOCACHE=/tmp/mcp-gateway-gocache go test -timeout=60s ./internal/tools` dan `GROQ_API_KEY= GOCACHE=/tmp/mcp-gateway-gocache go test -timeout=120s ./cmd/server ./internal/api` — semuanya lulus.
- [x] Perbarui `docs/api-contract.md`, `docs/tool-contracts/search_arxiv.json`, dan README setelah response aktual cocok dengan contoh.

**Fase selesai bila:** gateway mengembalikan jawaban atau abstensi ArXiv beserta evidence dan provenance yang benar; evaluasi memakai evidence yang sama atau berstatus `skipped`/`unavailable`; kegagalan downstream terlihat dan kontrak terjaga oleh checks.

## Fase 2 — Amankan evaluasi dan rapikan eksekusi

**Goal:** evaluasi menjadi diagnostik yang dapat dipercaya dan kode jalur aktif tidak ambigu.

- [x] Pisahkan `evaluation_status` dari status jawaban; skor yang hilang/tidak valid tidak menjadi `0`.
- [x] Definisikan perilaku evaluator untuk jawaban pendek, abstensi, dan evidence kosong bersama repo evaluator.
- [x] Lindungi jalur gateway dari skor menyesatkan: lewati abstensi/context kosong dan nilai jawaban pendek secara konservatif terhadap evidence.
- [x] Tambahkan versi evaluator ke response saat API evaluator mengirimkannya.
- [ ] Putuskan kebijakan retry. Untuk v1, biarkan retry nonaktif sampai alasan, input yang berubah, batas percobaan, dan format rekam percobaan disepakati.
- [x] Hapus `planner.MakePlan` dan `ExecuteWithRetry` yang tidak digunakan router agar tidak menjadi jalur eksekusi lama yang menyesatkan.
- [x] Pastikan executor tidak menggunakan retrieved chunks sebagai jawaban/context dan tidak mencampur evidence antar-backend.
- [x] Biarkan retry nonaktif sampai kebijakan, batas percobaan, input yang berubah, dan metadata retry disepakati.
- [x] Pastikan skor hanya dicatat ketika evaluasi sukses dan skornya valid.
- [x] Tambahkan coverage untuk evaluator sukses, HTTP 400/500, timeout, JSON rusak, respons terlalu besar, score hilang/salah tipe/di luar rentang, abstensi/evidence kosong, dan jawaban pendek.

**Selesai bila:** jawaban tetap dikembalikan saat evaluator gagal, response dan penyimpanan membedakan statusnya, dan tidak ada retry atau skor palsu.

## Fase 3 — Tambahkan `rag-research` sebagai backend terpisah

**Goal:** pengguna dapat memilih backend RAG secara eksplisit tanpa mengubah jalur ArXiv.

- [ ] Konfirmasi kontrak aktual `rag-research`: endpoint `/generate`, request, response, abstensi, confidence, dan struktur `retrieved_chunks`.
- [ ] Tentukan apakah evaluator gateway menerima output RAG; dokumentasikan semantik evidence yang akan diteruskan.
- [ ] Implementasikan adapter RAG terpisah dengan URL konfigurabel; jangan gunakan adapter `retrieve_chunks` lama sebagai asumsi kontrak.
- [ ] Daftarkan tool/backend RAG dan aktifkan pilihan `research_rag` hanya setelah adapter siap.
- [ ] Petakan jawaban, abstensi, evidence, ID chunk/sumber, serta failure ke kontrak bersama.
- [ ] Tambahkan contract test dan uji routing yang membuktikan permintaan ArXiv hanya memanggil ArXiv dan permintaan RAG hanya memanggil RAG.
- [ ] Tandai atau hapus `internal/tools/retrieve_chunks.go` jika tidak lagi menjadi fitur yang didukung.

**Selesai bila:** kedua backend dapat dipilih secara eksplisit, diuji dengan stub, dan setiap jawaban tetap dipasangkan dengan evidence backend asalnya.

## Fase 4 — Konfigurasi dan kesiapan operasional

**Goal:** gateway dapat dijalankan di lokal maupun lingkungan container dengan dependensi yang terlihat jelas.

- [ ] Buat konfigurasi terpusat untuk URL layanan, timeout per layanan, `POSTGRES_DSN`, dan parameter evaluator.
- [ ] Validasi konfigurasi saat startup dan dokumentasikan variabel lingkungan di `.env.example`.
- [ ] Gunakan satu HTTP client terkonfigurasi dengan batas waktu dan batas ukuran response; jangan menimpa deadline parent context.
- [ ] Tambahkan readiness terpisah dari liveness; laporkan dependency mana yang tersedia tanpa menyebut gateway siap penuh jika backend wajib tidak terjangkau.
- [ ] Tetapkan perilaku saat PostgreSQL tidak tersedia dan saat penulisan log gagal; jangan menghambat jawaban jika persistence bersifat opsional.
- [ ] Tambahkan satu jalur deployment terdokumentasi (Compose di repo ini atau panduan menjalankan layanan terpisah) termasuk jaringan, Ollama, dan volume yang diperlukan.
- [ ] Tinjau `POST /tool`: batasi nama tool/parameter yang boleh dipanggil pada deployment, serta tetapkan kebutuhan autentikasi dan rate limit.

**Selesai bila:** developer baru dapat menjalankan gateway dan dependensi yang didukung mengikuti instruksi, lalu membedakan liveness dari readiness.

## Fase 5 — Observability, privasi, dan dokumentasi rilis

**Goal:** satu request dapat ditelusuri dan klaim produk sesuai dengan implementasi.

- [ ] Propagasikan `request_id` ke adapter/downstream melalui header jika layanan mendukungnya.
- [ ] Catat backend, tool, status evaluasi, kategori error, dan durasi per tahap secara terstruktur.
- [ ] Tentukan redaksi dan retensi query/jawaban; hindari mencatat credential atau payload sensitif.
- [ ] Catat error persistence dengan cara yang bisa diamati tanpa membocorkan isi query.
- [ ] Perbarui README, `docs/architecture.md`, contoh curl, environment variables, dan benchmark setelah kontrak stabil.
- [ ] Hapus klaim Compose, benchmark, atau closed-loop yang belum bisa direproduksi dari source dan instruksi saat ini.

**Selesai bila:** dokumentasi dan contoh cocok dengan build yang ada; kegagalan lintas layanan dapat dilacak lewat satu ID tanpa menampilkan secret.

## Fase 6 — Putuskan cakupan MCP

**Goal:** nama dan klaim gateway sesuai dengan protokol yang benar-benar didukung.

- [ ] Untuk rilis awal, nyatakan HTTP/JSON sebagai antarmuka yang didukung.
- [ ] Jika protokol MCP menjadi kebutuhan, implementasikan transport dan discovery/invocation sesuai spesifikasi MCP.
- [ ] Tambahkan pemeriksaan interoperabilitas dengan client MCP sebelum mengklaim kompatibilitas.

**Selesai bila:** README menyebut HTTP saja, atau dukungan MCP telah dibuktikan lewat uji interoperabilitas protokol.

## Urutan kerja yang disarankan

1. Fase 1: selesaikan satu jalur ArXiv dengan evidence dan evaluator yang cocok.
2. Fase 2: rapikan evaluator, retry, dan planner lama.
3. Fase 4: konfigurasi dasar dan readiness agar jalur dapat dijalankan secara reproducible.
4. Fase 3: integrasikan backend RAG setelah kontrak downstream dipastikan.
5. Fase 5: observability dan dokumentasi rilis.
6. Fase 6: putuskan MCP sebagai fitur eksplisit, bukan asumsi dari tool registry.

## Bukan target rilis pertama

- Mengimplementasikan ulang retrieval atau generasi jawaban di Go.
- Menggabungkan jawaban dari beberapa backend dalam satu response.
- Menganggap skor faithfulness sebagai bukti kebenaran faktual.
- Menjalankan retry berdasarkan skor yang hilang atau belum tervalidasi.
- Mengklaim production-ready atau MCP-compatible tanpa bukti yang dapat diulang.
