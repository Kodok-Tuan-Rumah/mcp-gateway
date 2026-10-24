# Todo Berikutnya — MCP Gateway

Daftar ini dimulai setelah Phase 1A–1E selesai. Urutannya mengikuti dependency proyek: bereskan perilaku evaluator, siapkan konfigurasi operasional, baru aktifkan backend RAG kedua.

## Prioritas 1 — Phase 2: Amankan evaluasi dan rapikan eksekusi

**Goal:** skor evaluator dapat dipercaya sebagai informasi dukungan evidence, dan hanya ada satu jalur eksekusi aktif yang jelas.

### 2A. Selaraskan kontrak evaluator lintas-repo

- [x] Di `llm-eval-framework`, gunakan exact match konservatif ke evidence saat jawaban pendek tidak memiliki `ground_truth`; jangan membandingkan dengan string kosong.
- [x] Kembalikan `evaluator_version` dari versi evaluator yang benar-benar menjalankan request.
- [x] Pertahankan aturan abstensi/evidence kosong: gateway melewati evaluasi bila abstain atau context kosong; API evaluator menolak answer/context kosong.
- [x] Perbarui kontrak dan contoh di kedua repo; `evaluation.version` gateway hanya berasal dari `evaluator_version` API.

**Goal bagian ini:** gateway tahu kapan evaluasi bermakna, dan bisa mencatat versi evaluator tanpa mengarangnya.

### 2B. Kunci hasil evaluasi di gateway

- [x] Ganti decode `evaluate_answer` dari `map[string]interface{}` menjadi tipe response Go yang tervalidasi.
- [x] Petakan failure mode yang dikenal dan versi evaluator bila benar-benar dikembalikan.
- [x] Pastikan logging hanya menyimpan evaluasi sukses dengan score valid; evaluator gagal atau dilewati tidak boleh menghasilkan score `0`.
- [x] Tambahkan coverage untuk evaluator sukses, HTTP 400/500, timeout, JSON rusak, score hilang/salah tipe/di luar rentang, abstensi, evidence kosong, dan jawaban pendek.

**Goal bagian ini:** response dan penyimpanan menyatakan status evaluasi secara konsisten, termasuk untuk score nol yang sah.

### 2C. Hapus jalur eksekusi lama yang ambigu

- [x] Hapus `planner.MakePlan` dan `ExecuteWithRetry` yang tidak digunakan router.
- [x] Pastikan retrieved chunk tidak dapat menjadi jawaban atau bercampur dengan context evidence backend aktif.
- [x] Biarkan retry nonaktif sampai alasan retry, input yang berubah, batas percobaan, dan metadata percobaan disepakati.

**Goal bagian ini:** semua jalur jawaban melewati routing backend yang sama dan mempertahankan provenance.

## Prioritas 2 — Phase 4: Siapkan konfigurasi dan operasi

**Goal:** gateway bisa dijalankan secara konsisten di lokal maupun container dan kegagalan dependency terlihat jelas.

- [ ] Buat konfigurasi terpusat untuk URL service, timeout per service, PostgreSQL, dan parameter evaluator.
- [ ] Validasi konfigurasi saat startup dan dokumentasikan semua environment variable di `.env.example`.
- [ ] Gunakan HTTP client bersama dengan timeout, batas response, dan penghormatan terhadap deadline parent.
- [ ] Tambahkan readiness check terpisah dari liveness; laporkan dependency yang tidak tersedia.
- [ ] Tentukan perilaku ketika PostgreSQL tidak tersedia dan buat error logging persistence dapat diamati tanpa membocorkan query.
- [ ] Dokumentasikan satu cara menjalankan gateway dan dependency-nya, termasuk jaringan ke backend serta Ollama.
- [ ] Tinjau keamanan `/tool`: pembatasan tool/parameter, autentikasi, dan rate limit.

**Goal tercapai bila:** developer dapat menjalankan stack mengikuti instruksi dan membedakan gateway hidup dari dependency siap.

## Prioritas 3 — Phase 3: Tambahkan backend `research_rag`

**Prasyarat:** kontrak `/generate` diverifikasi terhadap source terbaru di repo `rag-research`.

**Goal:** client dapat memilih `arxiv` atau `research_rag` secara eksplisit tanpa mencampur hasil keduanya.

- [ ] Catat request/response `/generate`, abstention, confidence, latency, model, dan struktur `retrieved_chunks`.
- [ ] Putuskan apakah evaluator gateway berlaku untuk jawaban RAG dan bagaimana evidence disusun menjadi context.
- [ ] Buat adapter RAG terpisah dengan URL, timeout, validasi, dan batas body yang dikonfigurasi.
- [ ] Petakan jawaban, evidence, source/chunk ID, latency, abstention, dan error ke kontrak publik yang sama.
- [ ] Aktifkan `research_rag` hanya setelah adapter dan validasinya siap.
- [ ] Tambahkan stub acceptance checks yang membuktikan request ArXiv hanya memanggil ArXiv dan request RAG hanya memanggil RAG.
- [ ] Hapus atau tandai `internal/tools/retrieve_chunks.go` sebagai legacy bila tidak menjadi bagian kontrak baru.

**Goal tercapai bila:** kedua backend bisa dipilih dan diuji secara terpisah, serta evidence setiap jawaban berasal dari backend yang menjawab.

## Prioritas 4 — Phase 5: Observability, privasi, dan dokumentasi rilis

**Goal:** request dapat ditelusuri dan dokumentasi tidak mengklaim kemampuan yang belum tersedia.

- [ ] Teruskan `request_id` ke downstream lewat header bila kontrak service mendukungnya.
- [ ] Catat backend, tool, status evaluasi, kategori error, dan durasi per tahap secara terstruktur.
- [ ] Tentukan redaksi dan retensi query/jawaban; jangan catat credential atau payload sensitif.
- [ ] Selaraskan `PROJECT_GOALS.md`, `PROJECT_CONTEXT.md`, README, dan dokumen workspace dengan implementasi gateway terbaru.
- [ ] Perbarui contoh curl, arsitektur, environment variable, dan benchmark berdasarkan perilaku yang dapat direproduksi.
- [ ] Hapus klaim deployment, Compose, benchmark, atau closed-loop yang belum diverifikasi.

**Goal tercapai bila:** log dapat digunakan untuk menelusuri kegagalan tanpa membuka data sensitif, dan dokumentasi sesuai perilaku source.

## Prioritas 5 — Phase 6: Putuskan cakupan MCP

**Goal:** nama produk dan dokumentasi sesuai protokol yang benar-benar didukung.

- [ ] Untuk rilis awal, nyatakan HTTP/JSON sebagai antarmuka yang didukung.
- [ ] Jika MCP wire protocol dibutuhkan, implementasikan transport serta discovery/invocation sesuai spesifikasi.
- [ ] Uji interoperabilitas dengan MCP client sebelum menyatakan gateway kompatibel dengan MCP.

**Goal tercapai bila:** gateway dinyatakan sebagai HTTP gateway, atau dukungan MCP dibuktikan melalui uji interoperabilitas.

## Urutan mulai

1. Lanjutkan **2B** dengan coverage gateway untuk kegagalan/response evaluator, termasuk memastikan jawaban pendek diteruskan ke evaluator.
2. Kerjakan **2C** untuk menghapus atau mengisolasi planner lama.
3. Kerjakan **Phase 4** sebagai fondasi sebelum menjalankan beberapa backend bersama.
4. Implementasikan **Phase 3** setelah kontrak `/generate` diverifikasi.
5. Tutup dengan **Phase 5–6** sebelum menyatakan kesiapan rilis atau dukungan MCP.
