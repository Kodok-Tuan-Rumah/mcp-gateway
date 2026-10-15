# Todo Hari Ini — 15 Oktober 2026

## Target

Selesaikan penyelarasan desain lintas-repo (Phase 0), lalu tinggalkan daftar gate yang jelas sebelum adapter backend kedua dan deployment bersama dikerjakan.

## Sudah dikerjakan

- [x] Baca arah workspace dan kontrak aktual ArXiv, `rag-research`, serta evaluator.
- [x] Catat pemilik tiap kapabilitas dan putuskan ArXiv sebagai jalur integrasi pertama; RAG menjadi backend jawaban terpisah.
- [x] Catat bentuk request/response, port, status abstensi, dan mismatch `/retrieve` vs `/generate`.
- [x] Temukan short-answer evaluator menggunakan `ground_truth` yang tidak tersedia di API HTTP.
- [x] Lewati evaluasi untuk abstensi/evidence kosong; jawaban pendek diteruskan ke evaluator untuk exact match konservatif.
- [x] Tetapkan target pemilihan backend eksplisit; default `arxiv` saat ini hanya perilaku transisi.
- [x] Tetapkan `mcp-gateway` sebagai pemilik definisi deployment bersama, dengan usulan lokasi `deploy/`.
- [x] Dokumentasikan hasil di [`docs/workspace-alignment.md`](docs/workspace-alignment.md) dan perbarui kontrak gateway.

## Tindak lanjut lintas-repo

- [ ] Selaraskan `llm-eval-framework` agar short answer dinilai berdasarkan evidence atau menerima ground truth yang sah; tambahkan versi evaluator ke response.
- [ ] Tetapkan alamat jaringan evaluator→Ollama dan gateway→backend saat merancang deployment container.
- [ ] Selaraskan dokumen arah workspace bersama: isinya masih menyebut adapter gateway hard-coded dan planner aktif dapat mencampur backend, padahal source gateway sudah berubah.

**Phase 0 source alignment selesai:** batas repo, target routing, kontrak aktual, dan pemilik deployment tercatat. Tindak lanjut di atas menjadi gate sebelum skor evaluator dipakai untuk keputusan dan sebelum deployment bersama.

## Berikutnya setelah Phase 0

- [x] Phase 1A: kunci kontrak wire ArXiv dan pemetaan source, score, latency, model, serta sentinel abstensi.
- [x] Phase 1B: validasi parameter dan schema response, cek error pembacaan body, batasi ukuran response hingga 4 MiB, dan klasifikasikan kegagalan downstream.
- [x] Phase 1C: teruskan evidence, model, dan latency dari hasil ArXiv ke `GatewayResponse`.
- [x] Pastikan evaluator menerima jawaban dan evidence dari respons ArXiv yang sama.
- [x] Phase 1D: pisahkan abstensi, error backend, evaluasi dilewati/gagal, dan validasi skor evaluasi.
- [x] Phase 1E: tambah acceptance checks adapter dan alur `/ask`/`/plan` dengan downstream stub, lalu selaraskan kontrak API, kontrak tool, dan README.

## Batas hari ini

Repo sibling hanya ditinjau secara read-only. Perubahan yang perlu dilakukan di `llm-eval-framework`, deployment bersama, atau arah workspace global dicatat sebagai tindak lanjut, bukan ditandai selesai di gateway.
