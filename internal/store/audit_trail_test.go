package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// TestAuditTrail_OneRequestOneRow verifies that LogAudit writes exactly one row
// to audit_trail per request. This is the P0-3 acceptance criterion:
// "1 request → 1 baris audit_trail".
//
// Requires a running PostgreSQL instance (TEST_POSTGRES_DSN env var).
func TestAuditTrail_OneRequestOneRow(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping audit trail integration test")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	oldDB := db
	newDB, _ := sql.Open("postgres", dsn)
	db = newDB
	defer func() { newDB.Close(); db = oldDB }()

	ctx := context.Background()

	// Ensure migrations are applied.
	if err := migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Count rows before.
	var before int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_trail`).Scan(&before)

	// Write one audit log entry.
	testTenant := "tenant-test-uuid-12345"
	LogAudit(ctx, AuditLog{
		RequestID: "test-req-001",
		Backend:   "research_rag",
		Claim:     "test claim",
		Decision:  "answer",
		TenantID:  testTenant,
	})

	// Count rows after.
	var after int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_trail`).Scan(&after)

	if after != before+1 {
		t.Errorf("expected %d rows after LogAudit, got %d (before=%d)", before+1, after, before)
	}

	// Verify the tenant_id was stored correctly (as TEXT, not UUID FK).
	var storedTenant string
	err = db.QueryRowContext(ctx, `SELECT tenant_id FROM audit_trail WHERE request_id='test-req-001'`).Scan(&storedTenant)
	if err != nil {
		t.Fatalf("query tenant_id: %v", err)
	}
	if storedTenant != testTenant {
		t.Errorf("stored tenant_id=%q, want %q", storedTenant, testTenant)
	}

	// Clean up.
	db.ExecContext(ctx, `DELETE FROM audit_trail WHERE request_id='test-req-001'`)
}

// TestAuditTrail_TenantIDText verifies that audit_trail.tenant_id column
// accepts TEXT values (not just UUIDs), preventing FK violations.
func TestAuditTrail_TenantIDText(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	oldDB := db
	newDB2, _ := sql.Open("postgres", dsn)
	db = newDB2
	defer func() { newDB2.Close(); db = oldDB }()

	ctx := context.Background()

	if err := migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Verify column type is TEXT.
	var dataType string
	err = db.QueryRowContext(ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_name = 'audit_trail' AND column_name = 'tenant_id'`).Scan(&dataType)
	if err != nil {
		t.Fatalf("query column type: %v", err)
	}
	if dataType != "text" {
		t.Errorf("audit_trail.tenant_id type=%q, want text", dataType)
	}
}
