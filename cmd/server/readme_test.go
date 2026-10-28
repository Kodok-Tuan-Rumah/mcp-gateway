package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mengembalikan README.md
func readme(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("baca README.md: %v", err)
	}
	return string(b)
}

func TestReadme_ReferencesOnlyExistingPaths(t *testing.T) {
	content := readme(t)
	backticked := regexp.MustCompile("`([A-Za-z0-9_./-]+\\.(?:go|md|yml|yaml|json|sh|py))`")
	treeLine := regexp.MustCompile(`(?m)^[│├└─\s]*([A-Za-z0-9_.-]+\.(?:go|md|yml|yaml|json|sh|py))\b`)
	sourceDirs := []string{
		"cmd/server",
		"internal", "internal/api", "internal/config", "internal/planner",
		"internal/registry", "internal/store", "internal/tools",
		"scripts", "docs",
	}

	seen := map[string]bool{}

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

// README should document only runtime settings parsed by gateway config.
func TestReadme_DocumentsOnlyRealEnvVars(t *testing.T) {
	content := readme(t)
	known := map[string]struct{}{
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

	envPattern := regexp.MustCompile("`([A-Z][A-Z0-9_]{3,})=")
	for _, m := range envPattern.FindAllStringSubmatch(content, -1) {
		name := m[1]
		if _, ok := known[name]; !ok {
			t.Errorf("README mendokumentasikan %s yang tidak dibaca config", name)
		}
	}

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

func TestReadme_ExampleUsesCurrentContract(t *testing.T) {
	content := readme(t)
	if !strings.Contains(content, "request_id") {
		t.Error("contoh response di README tidak memakai request_id")
	}
	if !strings.Contains(content, "evaluation_status") {
		t.Error("contoh response di README tidak memakai evaluation_status")
	}

	for _, stale := range []string{`"Answer"`, `"Score"`, `"Query"`} {
		if strings.Contains(content, stale) {
			t.Errorf("contoh response masih memakai key lama %s", stale)
		}
	}
}
