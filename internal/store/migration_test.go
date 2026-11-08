package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

func testDSN(t *testing.T) string {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping migration test")
	}
	return dsn
}

// TestMigrations_Idempotent runs migrations twice and verifies they are a
// no-op the second time. It also verifies schema_migrations is populated.
func TestMigrations_Idempotent(t *testing.T) {
	dsn := testDSN(t)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Drop schema_migrations if exists so we start clean (test-only).
	db.ExecContext(ctx, `DROP TABLE IF EXISTS schema_migrations CASCADE`)

	// First run: apply all migrations.
	if err := migrate(); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	// Verify all expected versions are recorded.
	expected := []string{"001", "002", "003", "004", "005", "006", "007"}
	for _, v := range expected {
		var found string
		err := db.QueryRowContext(ctx, `SELECT version FROM schema_migrations WHERE version=$1`, v).Scan(&found)
		if err != nil {
			t.Errorf("migration %s not recorded in schema_migrations: %v", v, err)
		}
		if found != v {
			t.Errorf("expected version %s, got %s", v, found)
		}
	}

	// Second run: should be a complete no-op (all checksums match).
	if err := migrate(); err != nil {
		t.Fatalf("second migrate (should be no-op): %v", err)
	}

	// Verify no duplicate rows were inserted.
	var count int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
	if count != len(expected) {
		t.Errorf("expected %d schema_migrations rows after idempotent re-run, got %d", len(expected), count)
	}
}
