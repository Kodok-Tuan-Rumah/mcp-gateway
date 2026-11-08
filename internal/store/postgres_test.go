package store

import (
	"context"
	"strings"
	"testing"
)

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
func TestNullIfEmpty(t *testing.T) {
	if got := nullIfEmpty(""); got != nil {
		t.Errorf("string kosong harus jadi nil, dapat %#v", got)
	}
	if got := nullIfEmpty("rule-based"); got != "rule-based" {
		t.Errorf("string isi harus tetap utuh, dapat %#v", got)
	}
}

// ListAuditReports dengan org_id kosong tidak boleh mengembalikan semua report
// (tidak ada $1='' bypass). Harus mengembalikan hasil kosong tanpa menyentuh DB.
func TestListAuditReports_EmptyOrgReturnsNone(t *testing.T) {
	items, err := ListAuditReports(context.Background(), "", 20, 0)
	if err != nil {
		t.Fatalf("org_id kosong harus tidak error, dapat: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("org_id kosong harus menghasilkan 0 report, dapat %d", len(items))
	}
}

// GetAuditReport dengan tenant_id kosong harus ErrNotFound tanpa menyentuh DB.
func TestGetAuditReport_EmptyTenantIsNotFound(t *testing.T) {
	_, err := GetAuditReport(context.Background(), "00000000-0000-0000-0000-000000000000", "")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound for empty tenant, got %v", err)
	}
}

// RevokeShareToken dengan tenant_id kosong harus error, bukan menyentuh DB.
func TestRevokeShareToken_EmptyTenantErrors(t *testing.T) {
	err := RevokeShareToken(context.Background(), "00000000-0000-0000-0000-000000000000", "")
	if err == nil {
		t.Error("expected error for empty tenant, got nil")
	}
}

func TestLogTable_IsGatewayOwned(t *testing.T) {
	if LogTable != "gateway_query_logs" {
		t.Errorf("LogTable = %q, harus gateway_query_logs", LogTable)
	}
	if LogTable == "query_logs" {
		t.Error("gateway tidak boleh menulis ke query_logs milik backend")
	}
}

// LogEval harus aman dipanggil tanpa koneksi: logging adalah jalur
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
