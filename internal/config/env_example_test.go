package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEnvExample_ListsEverySupportedVariable(t *testing.T) {
	// Setiap env var yang dibaca LoadFromLookup.
	supported := map[string]struct{}{
		"GATEWAY_LISTEN_ADDRESS":   {},
		"POSTGRES_DSN":             {},
		"GROQ_API_KEY":             {},
		"GROQ_MODEL":               {},
		"ARXIV_SERVICE_URL":        {},
		"EVAL_SERVICE_URL":         {},
		"RAG_SERVICE_URL":          {},
		"GATEWAY_REQUEST_TIMEOUT":  {},
		"STORE_LOG_TIMEOUT":        {},
		"ARXIV_TIMEOUT":            {},
		"EVAL_TIMEOUT":             {},
		"RAG_TIMEOUT":              {},
		"ARXIV_MAX_RESPONSE_BYTES": {},
		"EVAL_MAX_RESPONSE_BYTES":  {},
		"RAG_MAX_RESPONSE_BYTES":   {},
	}

	documented := parseEnvExample(t)

	for name := range supported {
		if _, ok := documented[name]; !ok {
			t.Errorf("env var %s dibaca config tapi tidak ada di .env.example", name)
		}
	}

	for name := range documented {
		if _, ok := supported[name]; !ok {
			t.Errorf(".env.example mendokumentasikan %s yang tidak dibaca config", name)
		}
	}
}
func TestEnvExample_ValuesAreParseable(t *testing.T) {
	for name, value := range parseEnvExample(t) {
		if value == "" {
			t.Errorf("%s ada di .env.example tapi kosong", name)
			continue
		}
		// Duration harus valid.
		switch name {
		case "GATEWAY_REQUEST_TIMEOUT", "STORE_LOG_TIMEOUT",
			"ARXIV_TIMEOUT", "EVAL_TIMEOUT", "RAG_TIMEOUT":
			if _, err := time.ParseDuration(value); err != nil {
				t.Errorf("%s=%q bukan duration Go yang valid", name, value)
			}
		case "ARXIV_MAX_RESPONSE_BYTES", "EVAL_MAX_RESPONSE_BYTES", "RAG_MAX_RESPONSE_BYTES":
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				t.Errorf("%s=%q bukan bilangan bulat positif", name, value)
			}
		case "ARXIV_SERVICE_URL", "EVAL_SERVICE_URL", "RAG_SERVICE_URL":
			if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
				t.Errorf("%s=%q tidak punya skema http", name, value)
			}
		}
	}
}

func TestEnvExample_ExampleValuesMatchDefaults(t *testing.T) {
	documented := parseEnvExample(t)
	want := Default()

	checks := map[string]string{
		"GATEWAY_LISTEN_ADDRESS":   want.ListenAddress,
		"ARXIV_SERVICE_URL":        want.Arxiv.BaseURL,
		"EVAL_SERVICE_URL":         want.Evaluator.BaseURL,
		"RAG_SERVICE_URL":          want.RAG.BaseURL,
		"ARXIV_MAX_RESPONSE_BYTES": "4194304",
		"EVAL_MAX_RESPONSE_BYTES":  "1048576",
		"RAG_MAX_RESPONSE_BYTES":   "4194304",
	}

	for name, wantValue := range checks {
		got, ok := documented[name]
		if !ok {
			t.Errorf("%s tidak ada di .env.example", name)
			continue
		}
		if got != wantValue {
			t.Errorf("%s=%q di .env.example, default kode %q", name, got, wantValue)
		}
	}
}

// parseEnvExample membaca .env.example dan mengembalikan pasangan nama→nilai.
// Baris komentar, baris kosong, dan baris tanpa "=" diabaikan.
func parseEnvExample(t *testing.T) map[string]string {
	t.Helper()

	f, err := os.Open("../../.env.example")
	if err != nil {
		t.Fatalf("buka .env.example: %v", err)
	}
	defer func() { _ = f.Close() }()

	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("baca .env.example: %v", err)
	}
	if len(out) == 0 {
		t.Fatal(".env.example tidak berisi env var apa pun")
	}
	return out
}
