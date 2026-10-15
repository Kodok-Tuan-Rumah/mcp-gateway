package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Test ini menjaga README tetap jujur. README pernah mengklaim ada
// docker-compose.yml yang tidak pernah ada, dan menampilkan benchmark yang
// terukur ke endpoint yang tidak ada — keduanya terlihat kredibel tapi salah.
//
// Isinya memeriksa klaim yang bisa dibuktikan terhadap repo ini, bukan
// menyusun README. Kekhawatiran dokumentasi yang lain belum bisa diuji.

// readme mengembalikan isi README.md.
func readme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("baca README.md: %v", err)
	}
	return string(b)
}

// README tidak boleh menyebut file yang tidak ada. Klaim "docker-compose.yml —
// full stack (10 services)" bertahan lama karena tidak ada yang memeriksanya.
// Nama file di pohon direktori tidak memakai backtick, jadi keduanya dipindai.
func TestReadme_ReferencesOnlyExistingPaths(t *testing.T) {
	content := readme(t)

	// (1) Rujukan inline di dalam backtick, mis. `.env.example`.
	backticked := regexp.MustCompile("`([A-Za-z0-9_./-]+\\.(?:go|md|yml|yaml|json|sh|py))`")

	// (2) Baris pohon direktori, mis. "├── docker-compose.yml — full stack".
	// Ekstensi yang umum dipakai, tanpa tanda backtick.
	treeLine := regexp.MustCompile(`(?m)^[│├└─\s]*([A-Za-z0-9_.-]+\.(?:go|md|yml|yaml|json|sh|py))\b`)

	// Subtree tempat file sumber berada. Baris pohon sering hanya menulis
	// basename, jadi nama harus dicari di repo root dulu, lalu di sini.
	sourceDirs := []string{
		"cmd/server",
		"internal", "internal/api", "internal/config", "internal/planner",
		"internal/registry", "internal/store", "internal/tools",
		"internal/orchestrator",
		"scripts", "docs",
	}

	seen := map[string]bool{}

	// exists melapor apakah ref menunjuk file yang benar-benar ada.
	exists := func(ref string) bool {
		if _, err := os.Stat(filepath.Join("..", "..", ref)); err == nil {
			return true
		}
		if strings.Contains(ref, "/") {
			return false // path berfolder sudah Persis, tidak perlu ditebak
		}
		for _, dir := range sourceDirs {
			if _, err := os.Stat(filepath.Join("..", "..", dir, ref)); err == nil {
				return true
			}
		}
		return false
	}

	check := func(ref string) {
		if seen[ref] {
			return
		}
		seen[ref] = true
		if !exists(ref) {
			t.Errorf("README menyebut file yang tidak ada: %s", ref)
		}
	}

	for _, m := range backticked.FindAllStringSubmatch(content, -1) {
		check(m[1])
	}
	for _, m := range treeLine.FindAllStringSubmatch(content, -1) {
		check(m[1])
	}

	if len(seen) == 0 {
		t.Error("tidak ada referensi file sama sekali — regex kemungkinan rusak")
	}
}

// Setiap env var yang didokumentasikan README harus benar-benar dibaca kode.
// README pernah mencantumkan RAG_SERVICE_URL/EVAL_SERVICE_URL/ARXIV_SERVICE_URL
// sementara kode hanya membaca GROQ_API_KEY dan POSTGRES_DSN.
func TestReadme_DocumentsOnlyRealEnvVars(t *testing.T) {
	content := readme(t)

	known := map[string]struct{}{
		"GROQ_API_KEY":             {},
		"GROQ_MODEL":               {},
		"POSTGRES_DSN":             {},
		"ARXIV_SERVICE_URL":        {},
		"EVAL_SERVICE_URL":         {},
		"RAG_SERVICE_URL":          {},
		"GATEWAY_LISTEN_ADDRESS":   {},
		"GATEWAY_REQUEST_TIMEOUT":  {},
		"STORE_LOG_TIMEOUT":        {},
		"ARXIV_TIMEOUT":            {},
		"EVAL_TIMEOUT":             {},
		"RAG_TIMEOUT":              {},
		"ARXIV_MAX_RESPONSE_BYTES": {},
		"EVAL_MAX_RESPONSE_BYTES":  {},
		"RAG_MAX_RESPONSE_BYTES":   {},
	}

	// Cocokkan hanya nama env var: huruf kapital semua, di dalam backtick, dan
	// ditulis sebagai NAME= (pola lama menuntut "=` sehingga nama yang ditulis
	// "__NAME__=" http://..." justru terlewat).
	envPattern := regexp.MustCompile("`([A-Z][A-Z0-9_]{3,})=")
	for _, m := range envPattern.FindAllStringSubmatch(content, -1) {
		name := m[1]
		if _, ok := known[name]; !ok {
			t.Errorf("README mendokumentasikan %s yang tidak dibaca config", name)
		}
	}

	// Nama env var juga boleh disebut tanpa nilai, misalnya "RAG_SERVICE_URL is
	// not consumed". Pola ini hanya menangkap yang bersama tanda kurung.
	mentionPattern := regexp.MustCompile("`([A-Z][A-Z0-9_]{3,})`")
	for _, m := range mentionPattern.FindAllStringSubmatch(content, -1) {
		name := m[1]
		if !envVarish(name) {
			continue
		}
		if _, ok := known[name]; !ok {
			t.Errorf("README menyebut %s yang tidak dibaca config", name)
		}
	}
}

// envVarish menyaring nama kapital yang merupakan nama konfigurasi: berakhiran
// _URL/_DSN/_KEY/_ADDRESS/_TIMEOUT, atau memuat SERVICE/BYTES/LISTEN. Tanpa
// penyaring ini nama file seperti PROJECT_GOALS ikut dianggap env var.
func envVarish(name string) bool {
	for _, suffix := range []string{"_URL", "_DSN", "_KEY", "_ADDRESS", "_TIMEOUT"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return strings.Contains(name, "SERVICE") ||
		strings.Contains(name, "BYTES") ||
		strings.Contains(name, "LISTEN")
}

// README harus menyebut make check dan make demo, karena itu jalur verifikasi
// yang dipakai Anyone untuk memastikan perubahan bekerja.
func TestReadme_PointsAtVerificationCommands(t *testing.T) {
	content := readme(t)
	for _, cmd := range []string{"make check", "make demo"} {
		if !strings.Contains(content, cmd) {
			t.Errorf("README tidak menyebut %q", cmd)
		}
	}
}

// /health hanya melaporkan liveness. README harus tidak menjanjikan readiness.
func TestReadme_DoesNotClaimReadiness(t *testing.T) {
	content := readme(t)
	for _, forbidden := range []string{
		"all services healthy",
		"readiness: ok",
		"fully operational",
		"production-ready",
		"production ready",
	} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Errorf("README mengklaim %q — belum ada mekanisme yang membuktikannya", forbidden)
		}
	}
}

// Jika README menampilkan response contoh, key-nya harus sesuai kontrak publik.
// Bentuk lama (Query/Answer/Score PascalCase) sudah tidak berlaku.
func TestReadme_ExampleUsesCurrentContract(t *testing.T) {
	content := readme(t)

	// Key kontrak wajib ada di contoh response.
	if !strings.Contains(content, "request_id") {
		t.Error("contoh response di README tidak memakai request_id")
	}
	if !strings.Contains(content, "evaluation_status") {
		t.Error("contoh response di README tidak memakai evaluation_status")
	}

	// Key lama tidak boleh muncul sebagai contoh field response.
	for _, stale := range []string{`"Answer"`, `"Score"`, `"Query"`} {
		if strings.Contains(content, stale) {
			t.Errorf("contoh response masih memakai key lama %s", stale)
		}
	}
}
