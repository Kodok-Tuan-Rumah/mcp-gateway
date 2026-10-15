package store

import (
	"context"
	"strings"
	"testing"
)

// Faithfulness harus *float64 supaya "tidak dinilai" tersimpan sebagai NULL.
// Dengan float64, tidak dinilai menjadi 0 dan tabel penuh angka nol yang
// terbaca sebagai hasil buruk.
func TestEvalLog_FaithfulnessIsNullable(t *testing.T) {
	var unset EvalLog
	if unset.Faithfulness != nil {
		t.Error("Faithfulness harus mulai sebagai nil")
	}

	zero := 0.0
	withZero := EvalLog{Faithfulness: &zero}
	if withZero.Faithfulness == nil {
		t.Error("skor nol yang sah harus tetap bisa disimpan")
	}
	if *withZero.Faithfulness != 0 {
		t.Error("nilai harus 0")
	}
}

// nullIfEmpty mengubah string kosong menjadi NULL supaya kolom tidak menyimpan
// string kosong yang terbaca sebagai nilai.
func TestNullIfEmpty(t *testing.T) {
	if got := nullIfEmpty(""); got != nil {
		t.Errorf("string kosong harus jadi nil, dapat %#v", got)
	}
	if got := nullIfEmpty("groq"); got != "groq" {
		t.Errorf("string isi harus tetap utuh, dapat %#v", got)
	}
}

// Nama tabel harus milik gateway. Tabel query_logs dimiliki
// arxiv-research-assistant dengan definisi kolom yang berbeda, dan menulis
// keduanya dengan semantik berbeda membuat hasil analitis tidak dipercaya.
func TestLogTable_IsGatewayOwned(t *testing.T) {
	if LogTable != "gateway_query_logs" {
		t.Errorf("LogTable = %q, harus gateway_query_logs", LogTable)
	}
	if LogTable == "query_logs" {
		t.Error("gateway tidak boleh menulis ke query_logs milik backend")
	}
}

// LogEval harus aman dipanggil tanpa koneksi: logging adalah jalur
// observability dan tidak boleh menjatuhkan request.
func TestLogEval_NoConnectionIsNoop(_ *testing.T) {
	original := db
	db = nil
	defer func() { db = original }()

	// Tidak panic, tidak error.
	LogEval(context.Background(), EvalLog{Query: "apa itu RAG?", Faithfulness: nil})
}

// Migrasi harus tertanam di binary dan memuat tabel milik gateway.
func TestMigrationsEmbedded(t *testing.T) {
	body, err := migrationsFS.ReadFile("migrations/001_gateway_query_logs.sql")
	if err != nil {
		t.Fatalf("baca migrasi: %v", err)
	}
	text := string(body)

	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS gateway_query_logs",
		"evaluation_status",
		"orchestrator",
		"decision",
		"failure_type",
		"duration_ms",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("migrasi tidak menyebut %q", want)
		}
	}

	// Skor harus DOUBLE PRECISION nullable supaya nil tidak jadi 0.
	if !strings.Contains(text, "DOUBLE PRECISION") {
		t.Error("faithfulness harus DOUBLE PRECISION, bukan INTEGER")
	}
}
