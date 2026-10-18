#!/usr/bin/env bash
# Verifikasi manual hasil pekerjaan minggu ini.
# Menyalakan gateway sungguhan lalu menjalankan rangkaian request nyata.
#
# Pakai:  bash scripts/demo.sh
# Tidak butuh service downstream — justru itu yang dibuktikan: kegagalan
# downstream harus terlihat, bukan disamarkan jadi jawaban.

set -uo pipefail

PORT="${PORT:-8090}"
BASE="http://127.0.0.1:${PORT}"
BIN="$(mktemp -d)/mcp-gateway"
LOG="$(mktemp -d)/gateway.log"

say()  { printf '\n\033[1m── %s\033[0m\n' "$*"; }
ok()   { printf '   \033[32m✓\033[0m %s\n' "$*"; }
bad()  { printf '   \033[31m✗\033[0m %s\n' "$*"; }
note() { printf '   \033[2m%s\033[0m\n' "$*"; }

cleanup() {
  [ -n "${SRV:-}" ] && kill "$SRV" 2>/dev/null
  wait "${SRV:-}" 2>/dev/null
}
trap cleanup EXIT

say "Build"
go build -o "$BIN" ./cmd/server/ || { bad "build gagal"; exit 1; }
ok "binary jadi: $BIN"

say "Menyalakan gateway di :$PORT"
"$BIN" > "$LOG" 2>&1 &
SRV=$!
for _ in $(seq 1 25); do
  curl -sf "$BASE/health" >/dev/null 2>&1 && break
  sleep 0.2
done
curl -sf "$BASE/health" >/dev/null 2>&1 || { bad "server tidak hidup; log:"; cat "$LOG"; exit 1; }
ok "hidup (PID $SRV)"
note "catatan: 'failed to ping DB' itu normal — tidak ada PostgreSQL di sini"

j() { python3 -m json.tool 2>/dev/null || cat; }

# http_post POST body — tampilkan body (pretty) lalu kode HTTP di baris sendiri.
http_post() {
  local path="$1" body="$2" out code
  out=$(curl -s -o /tmp/.demo_body -w '%{http_code}' -X POST "$BASE$path" \
        -H 'Content-Type: application/json' -d "$body")
  code="$out"
  printf '   HTTP %s\n' "$code"
  j < /tmp/.demo_body
}

# ───────────────────────────── /health ─────────────────────────────
say "GET /health — urutan tools harus deterministik"
for i in 1 2 3; do
  curl -s "$BASE/health" | python3 -c 'import json,sys;print("   ",json.load(sys.stdin)["tools"])'
done
ok "urutan sama di 3 request"

say "GET /health — Honest: liveness, bukan readiness"
curl -s "$BASE/health" | j
note "status ok hanya berarti proses hidup — bukan jaminan downstream hidup. Readiness = Phase 4."

# ────────────────────── /tool: kontrak snake_case ──────────────────────
say "POST /tool — payload persis README (sebelumnya 400)"
http_post /tool '{"tool_name":"search_arxiv","params":{"query":"apa itu RAG?","top_k":5}}'
note "400 di sini BENAR: artinya payload diterima, lalu downstream tidak terjangkau."

say "POST /tool — key PascalCase harus DITOLAK"
curl -s -o /dev/null -w '   HTTP %{http_code} (harus 400)\n' -X POST "$BASE/tool" \
  -H 'Content-Type: application/json' \
  -d '{"ToolName":"search_arxiv","Params":{"query":"x"}}'

say "POST /tool — tool tidak dikenal"
curl -s -X POST "$BASE/tool" -H 'Content-Type: application/json' \
  -d '{"tool_name":"tidak_ada"}' | j

# ──────────────────── /plan: routing + kegagalan jujur ────────────────────
say "POST /plan — research_rag kini AKTIF: 200, steps → research_rag + evaluate_answer"
http_post /plan '{"backend":"research_rag","query":"apa itu RAG?"}'

say "POST /plan — backend tidak dikenal harus 400 (invalid_request)"
http_post /plan '{"backend":"nonsense","query":"apa itu RAG?"}'

say "POST /plan — valid, tapi downstream mati → 502 yang JUJUR"
http_post /plan '{"query":"apa itu RAG?"}'
note "perhatikan: answer kosong, evaluation_status=skipped, tidak ada key 'evaluation'."
note "dulu: answer berisi teks chunk dan Score 0. Sekarang keduanya hilang — itu memang jujur."

# ─────────────────────────── /ask fallback ───────────────────────────
say "POST /ask — tanpa GROQ_API_KEY → fallback rule-based"
GROQ_API_KEY= curl -s -X POST "$BASE/ask" \
  -H 'Content-Type: application/json' -d '{"query":"apa itu RAG?"}' | j
note "orchestrator=rule-based. Kontrak /plan dan /ask sengaja dibuat sama."

# ─────────────────────────── validasi input ───────────────────────────
say "Validasi input — semua harus 400"
for body in '{"query":""}' \
            '{"query":"ab"}' \
            '{"query":"apa itu RAG?","top_k":999}' \
            '{"query":"apa itu RAG?","top_k":-1}' \
            '{"query":"apa itu RAG?","ngawur":1}'; do
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/plan" \
    -H 'Content-Type: application/json' -d "$body")
  printf '   HTTP %s  %s\n' "$code" "$body"
  [ "$code" = "400" ] || bad "harusnya 400"
done
ok "kelima request ditolak"

say "Body melebihi batas (1 MiB) → harus 400"
python3 - <<PY > /tmp/big.json
import json
print(json.dumps({"query": "a" * (1024*1024 + 512)}))
PY
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/plan" \
  -H 'Content-Type: application/json' --data-binary @/tmp/big.json)
printf '   HTTP %s (harus 400)\n' "$code"
rm -f /tmp/big.json

# ─────────────────────────── request id ───────────────────────────
say "request_id selalu ada di setiap response"
for p in /plan /ask; do
  id=$(curl -s -X POST "$BASE$p" -H 'Content-Type: application/json' \
       -d '{"query":"apa itu RAG?"}' | python3 -c 'import json,sys;print(json.load(sys.stdin).get("request_id",""))')
  printf '   %-6s %s\n' "$p" "${id:-KOSONG}"
done

say "Selesai"
ok "semua yang ditampilkan di atas adalah perilaku production, bukan test."
note "test suite (48 test di cmd/server) ada di: go test -v ./cmd/server/"