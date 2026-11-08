package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"embed"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	_ "github.com/lib/pq" // postgres driver
)

// auditWriteFailuresTotal counts failed attempts to write audit/eval log rows.
// A non-zero value indicates the audit trail is dropping execution evidence —
// a critical failure for an audit product.
var auditWriteFailuresTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "mcp_gateway_audit_write_failures_total",
		Help: "Total number of failed audit/eval log writes",
	},
	[]string{"error_kind"},
)

func init() {
	prometheus.MustRegister(auditWriteFailuresTotal)
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DefaultPostgresDSN adalah nilai fallback untuk pengembangan lokal.
const DefaultPostgresDSN = "host=localhost port=5432 user=arxiv password=arxiv_secret dbname=arxiv_db sslmode=disable"

// Connection pool defaults.
const (
	DefaultMaxOpenConns    = 25
	DefaultMaxIdleConns    = 5
	DefaultConnMaxLifetime = 5 * time.Minute
	DefaultConnMaxIdleTime = 1 * time.Minute
)

// LogTable adalah tabel milik gateway.
//
// Tabel `query_logs` milik arxiv-research-assistant dan tidak dipakai di sini:
// go-backend mengisi model sebagai nama LLM dan retrieval_ms sebagai latensi
// milestone backend, sedangkan gateway butuh orchestrator, status evaluasi, dan
// latensi total. Satu kolom dengan dua definisi membuat hasil analitis tidak
// bisa dipercaya.
const LogTable = "gateway_query_logs"

var db *sql.DB

// ErrNotFound is returned when a requested report does not exist.
var ErrNotFound = errors.New("not found")

// Init membuka koneksi PostgreSQL dengan connection pooling.
//
// dsn diberikan pemanggil (diambil dari config saat startup) supaya store tidak
// membaca environment variable sendiri dan supaya nilai yang dipakai bisa
// diuji. Kalau dsn kosong, konfigurasi default lokal dipakai.
//
// Pool configuration uses sensible defaults (MaxOpenConns=25, MaxIdleConns=5,
// ConnMaxLifetime=5m, ConnMaxIdleTime=1m). Use InitWithPool for custom settings.
func Init(dsn string) {
	InitWithPool(dsn, DefaultMaxOpenConns, DefaultMaxIdleConns, DefaultConnMaxLifetime, DefaultConnMaxIdleTime)
}

// InitWithPool membuka koneksi PostgreSQL dengan konfigurasi pool kustom.
func InitWithPool(dsn string, maxOpen, maxIdle int64, maxLifetime, maxIdleTime time.Duration) {
	if dsn == "" {
		dsn = DefaultPostgresDSN
	}

	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Printf("[store] failed to open DB: %v", err)
		return
	}

	// Configure connection pool
	db.SetMaxOpenConns(int(maxOpen))
	db.SetMaxIdleConns(int(maxIdle))
	db.SetConnMaxLifetime(maxLifetime)
	db.SetConnMaxIdleTime(maxIdleTime)

	log.Printf("[store] pool configured: maxOpen=%d, maxIdle=%d, maxLifetime=%v, maxIdleTime=%v",
		maxOpen, maxIdle, maxLifetime, maxIdleTime)

	// sql.Open does not connect. Ping is the first real network call, so it must
	// be bounded: otherwise an unreachable PostgreSQL host can delay gateway
	// startup for the OS TCP timeout (minutes).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Printf("[store] failed to ping DB: %v", err)
		_ = db.Close()
		db = nil
		return
	}

	if err := migrate(); err != nil {
		// Skema yang tidak ada akan membuat setiap penulisan gagal, jadi lebih
		// baik dinonaktifkan sekarang dan dilaporkan.
		log.Printf("[store] migration failed, logging disabled: %v", err)
		_ = db.Close()
		db = nil
		return
	}

	log.Printf("[store] connected to PostgreSQL")
}

// applyMigrations runs embedded migration files in version order, using an
// advisory lock so only one instance migrates at a time. Each migration is
// recorded in schema_migrations with its SHA256 checksum; re-running is a
// no-op unless a migration's checksum changed.
//
// Migration files must be named NNN_description.sql where NNN is the version.
// Only "up" migrations are supported (no down).
func applyMigrations(files []embed.FS) error {
	// Collect all migration files from all embed sources.
	type migration struct {
		version string
		name    string
		body    string
	}
	var all []migration

	for _, fs := range files {
		entries, err := fs.ReadDir("migrations")
		if err != nil {
			return fmt.Errorf("read migrations dir: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			body, err := fs.ReadFile("migrations/" + entry.Name())
			if err != nil {
				return fmt.Errorf("read %s: %w", entry.Name(), err)
			}
			all = append(all, migration{
				version: extractVersion(entry.Name()),
				name:    entry.Name(),
				body:    string(body),
			})
		}
	}

	// Sort by version string (zero-padded names sort correctly).
	sort.Slice(all, func(i, j int) bool {
		return all[i].version < all[j].version
	})

	if len(all) == 0 {
		return nil
	}

	// Acquire advisory lock within a transaction so only one instance
	// migrates at a time. Lock is released automatically on commit/rollback.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin lock tx: %w", err)
	}
	var lockResult int
	if err := tx.QueryRow("SELECT pg_advisory_xact_lock(hashtext('mcp-gateway-schema-migrations'))").Scan(&lockResult); err != nil {
		tx.Rollback()
		return fmt.Errorf("acquire advisory lock: %w", err)
	}

	// Ensure schema_migrations tracking table exists.
	if _, err := tx.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			checksum   TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		tx.Rollback()
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Load already-applied versions + checksums.
	applied := make(map[string]string) // version -> checksum
	rows, err := tx.Query("SELECT version, checksum FROM schema_migrations")
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			tx.Rollback()
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = checksum
	}

	// Apply pending migrations.
	for _, m := range all {
		checksum := sha256sum(m.body)
		existing, ok := applied[m.version]
		if ok && existing == checksum {
			continue // already applied, unchanged
		}

		// Run migration + record in same transaction.
		if _, err := tx.Exec(m.body); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s (%s): %w", m.version, m.name, err)
		}
		if ok {
			// Checksum changed: re-record.
			if _, err := tx.Exec(`UPDATE schema_migrations SET checksum=$1, applied_at=NOW() WHERE version=$2`, checksum, m.version); err != nil {
				tx.Rollback()
				return fmt.Errorf("update schema_migrations %s: %w", m.version, err)
			}
		} else {
			if _, err := tx.Exec(`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`, m.version, checksum); err != nil {
				tx.Rollback()
				return fmt.Errorf("insert schema_migrations %s: %w", m.version, err)
			}
		}
		log.Printf("[store] migration applied: %s (checksum=%s)", m.name, checksum[:12])
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

// extractVersion returns the zero-padded version prefix from a migration
// filename like "001_gateway_query_logs.sql".
func extractVersion(name string) string {
	var v []byte
	for _, c := range name {
		if c >= '0' && c <= '9' {
			v = append(v, byte(c))
		} else {
			break
		}
	}
	return string(v)
}

// sha256sum returns the hex SHA256 of the given string.
func sha256sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// migrate runs all embedded migrations with version tracking and advisory lock.
func migrate() error {
	return applyMigrations([]embed.FS{migrationsFS})
}

// EvalLog adalah satu catatan request gateway.
//
// Faithfulness adalah *float64, bukan float64. Nilai nil berarti tidak dinilai
// dan disimpan sebagai NULL; memakai float64 akan mengubah "tidak dinilai"
// menjadi 0, dan angka nol itu akan terbaca sebagai hasil yang buruk.
type EvalLog struct {
	RequestID        string
	Query            string
	Answer           string
	Backend          string
	BackendModel     string
	Orchestrator     string
	Decision         string
	FailureType      string
	EvaluationStatus string
	Faithfulness     *float64
	FailureMode      string
	DurationMs       int
}

// LogEval menyimpan satu request ke tabel gateway.
//
// Kegagalan penulisan tidak boleh menggagalkan request: pencatatan adalah
// jalur observability, bukan jalur serving.
func LogEval(ctx context.Context, e EvalLog) {
	if db == nil {
		return
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO `+LogTable+`
			(request_id, query, answer, backend, backend_model, orchestrator,
			 decision, failure_type, evaluation_status, faithfulness,
			 failure_mode, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		nullIfEmpty(e.RequestID),
		e.Query,
		e.Answer,
		nullIfEmpty(e.Backend),
		nullIfEmpty(e.BackendModel),
		nullIfEmpty(e.Orchestrator),
		nullIfEmpty(e.Decision),
		nullIfEmpty(e.FailureType),
		nullIfEmpty(e.EvaluationStatus),
		e.Faithfulness, // *float64 → NULL saat nil, bukan 0
		nullIfEmpty(e.FailureMode),
		e.DurationMs,
	)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			auditWriteFailuresTotal.WithLabelValues("canceled").Inc()
			log.Printf("[store] insert skipped (%s): request=%s", err, e.RequestID)
			return
		}
		auditWriteFailuresTotal.WithLabelValues("db_error").Inc()
		log.Printf("[store] insert failed: %v", err)
		return
	}
	log.Printf("[store] logged | request=%s orchestrator=%s eval=%s", e.RequestID, e.Orchestrator, e.EvaluationStatus)
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// PoolStats returns connection pool statistics.
func PoolStats() sql.DBStats {
	if db == nil {
		return sql.DBStats{}
	}
	return db.Stats()
}

// AuditLog adalah satu catatan audit trail per request.
type AuditLog struct {
	RequestID            string
	Backend              string
	Claim                string
	Decision             string
	CalibratedConfidence *float64
	CalibratedDecision   string
	EvidenceJSON         string
	SpanJSON             string
	// TenantID is the caller's tenant (UUID). Empty means tenant-scoping is not
	// enforced for this record (e.g. auth disabled).
	TenantID string
}

// LogAudit menyimpan audit trail. Kegagalan tidak boleh menggagalkan request.
func LogAudit(ctx context.Context, a AuditLog) {
	if db == nil {
		return
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_trail
			(request_id, backend, claim, decision, calibrated_confidence,
			 calibrated_decision, evidence, span_level_attribution, tenant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9)`,
		nullIfEmpty(a.RequestID),
		nullIfEmpty(a.Backend),
		a.Claim,
		nullIfEmpty(a.Decision),
		a.CalibratedConfidence,
		nullIfEmpty(a.CalibratedDecision),
		nullIfEmpty(a.EvidenceJSON),
		nullIfEmpty(a.SpanJSON),
		nullIfEmpty(a.TenantID),
	)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			log.Printf("[store] audit insert skipped (%s): request=%s", err, a.RequestID)
			return
		}
		log.Printf("[store] audit insert failed: %v", err)
		return
	}
	log.Printf("[store] audit logged | request=%s backend=%s decision=%s", a.RequestID, a.Backend, a.Decision)
}

// SetShareToken stores a share token + TTL on a report.
func SetShareToken(ctx context.Context, reportID, token string, expiresAt time.Time) error {
	if db == nil {
		return errors.New("db not initialized")
	}
	_, err := db.ExecContext(ctx, `UPDATE audit_reports SET share_token=$1, share_expires_at=$2 WHERE id=$3`, token, expiresAt, reportID)
	return err
}

// GetAuditReportByShareToken fetches a report (read-only) by share token if not expired and not revoked.
func GetAuditReportByShareToken(ctx context.Context, token string) (map[string]interface{}, error) {
	if db == nil {
		return nil, errors.New("db not initialized")
	}
	var id string
	var expires sql.NullTime
	var revoked bool
	err := db.QueryRowContext(ctx, `SELECT id, share_expires_at, share_revoked FROM audit_reports WHERE share_token=$1`, token).Scan(&id, &expires, &revoked)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if revoked {
		return nil, ErrNotFound
	}
	if expires.Valid && time.Now().After(expires.Time) {
		return nil, ErrNotFound
	}
	return getAuditReportByID(ctx, id)
}

// RevokeShareToken soft-revokes a share token so /r/{token} returns 404.
// Only the report's owner tenant may revoke.
func RevokeShareToken(ctx context.Context, reportID, tenantID string) error {
	if tenantID == "" {
		return ErrNotFound
	}
	if db == nil {
		return errors.New("db not initialized")
	}
	result, err := db.ExecContext(ctx, `UPDATE audit_reports SET share_revoked=TRUE WHERE id=$1 AND org_id=$2`, reportID, tenantID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListAuditReports returns reports owned by the caller's tenant, newest first.
// An empty orgID returns no reports (never all reports).
func ListAuditReports(ctx context.Context, orgID string, limit, offset int) ([]map[string]interface{}, error) {
	if orgID == "" {
		return []map[string]interface{}{}, nil
	}
	if db == nil {
		return nil, errors.New("db not initialized")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT id, status, claim_count, created_at
		FROM audit_reports WHERE org_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := db.QueryContext(ctx, q, orgID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]interface{}{}
	for rows.Next() {
		var id, status string
		var claimCount int
		var created time.Time
		if err := rows.Scan(&id, &status, &claimCount, &created); err != nil {
			continue
		}
		out = append(out, map[string]interface{}{
			"report_id":  id,
			"status":     status,
			"claim_count": claimCount,
			"created_at": created,
		})
	}
	return out, nil
}

func Close() {
	if db != nil {
		_ = db.Close()
	}
}

// AuditReport is a persisted audit dossier.
type AuditReport struct {
	ID               string
	APIKeyTier       string
	Status           string
	ClaimCount       int
	Summary          string
	ShareToken       string
	ShareExpiresAt   *time.Time
	CreatedAt        time.Time
}

// CreateAuditReport inserts a new report and its claims. Returns report id and share token.
// status is one of "completed", "partial", "failed".
func CreateAuditReport(ctx context.Context, orgID, tier, status string, claims []map[string]interface{}, summary map[string]interface{}) (string, string, error) {
	if db == nil {
		return "", "", errors.New("db not initialized")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()

	// generate share token (128-bit via crypto/rand) — done by caller ideally; here use DB gen.
	var reportID string
	summaryJSON, _ := jsonMarshal(summary)
	err = tx.QueryRowContext(ctx, `
		INSERT INTO audit_reports (org_id, api_key_tier, status, claim_count, summary)
		VALUES ($1, $2, $5, $3, $4::jsonb)
		RETURNING id`, nullIfEmpty(orgID), nullIfEmpty(tier), len(claims), summaryJSON, status).Scan(&reportID)
	if err != nil {
		return "", "", err
	}

	for i, c := range claims {
		ev, _ := jsonMarshal(c["evidence"])
		sa, _ := jsonMarshal(c["span_attribution"])
		evl, _ := jsonMarshal(c["evaluation"])
		_, err = tx.ExecContext(ctx, `
			INSERT INTO audit_report_claims (report_id, claim_index, claim, decision, confidence_zone,
				calibrated_confidence, failure_type, evidence, span_attribution, evaluation)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10::jsonb)`,
			reportID, i, c["claim"], c["decision"], c["confidence_zone"],
			c["calibrated_confidence"], c["failure_type"], ev, sa, evl)
		if err != nil {
			return "", "", err
		}
	}

	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return reportID, "", nil
}

// getAuditReportByID fetches a report by id without tenant filtering. Used only
// by the public share-token path (/r/{token}), where the token itself is the
// authorization.
func getAuditReportByID(ctx context.Context, id string) (map[string]interface{}, error) {
	if db == nil {
		return nil, errors.New("db not initialized")
	}
	row := db.QueryRowContext(ctx, `SELECT id, api_key_tier, status, claim_count, summary, share_token, share_expires_at, created_at FROM audit_reports WHERE id=$1`, id)
	return scanAuditReport(ctx, row)
}

// GetAuditReport fetches a report owned by the given tenant. Returns ErrNotFound
// when the report does not exist or is owned by another tenant (no enumeration).
func GetAuditReport(ctx context.Context, id, tenantID string) (map[string]interface{}, error) {
	if tenantID == "" {
		return nil, ErrNotFound
	}
	if db == nil {
		return nil, errors.New("db not initialized")
	}
	row := db.QueryRowContext(ctx, `SELECT id, api_key_tier, status, claim_count, summary, share_token, share_expires_at, created_at FROM audit_reports WHERE id=$1 AND org_id=$2`, id, tenantID)
	return scanAuditReport(ctx, row)
}

func scanAuditReport(ctx context.Context, row *sql.Row) (map[string]interface{}, error) {
	var r AuditReport
	var tier sql.NullString
	var summary sql.NullString
	var shareToken sql.NullString
	var shareExpires sql.NullTime
	err := row.Scan(&r.ID, &tier, &r.Status, &r.ClaimCount, &summary, &shareToken, &shareExpires, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r.APIKeyTier = tier.String
	r.Summary = summary.String
	r.ShareToken = shareToken.String
	if shareExpires.Valid {
		r.ShareExpiresAt = &shareExpires.Time
	}

	// fetch claims
	var claims []map[string]interface{}
	rows2, err := db.QueryContext(ctx, `SELECT claim_index, claim, decision, confidence_zone, calibrated_confidence, failure_type, evidence::text, span_attribution::text, evaluation::text FROM audit_report_claims WHERE report_id=$1 ORDER BY claim_index`, r.ID)
	if err == nil && rows2 != nil {
		defer rows2.Close()
		for rows2.Next() {
			var ci int
			var claim, decision, zone, ft string
			var conf sql.NullFloat64
			var ev, sa, evl sql.NullString
			if err := rows2.Scan(&ci, &claim, &decision, &zone, &conf, &ft, &ev, &sa, &evl); err != nil {
				continue
			}
			claims = append(claims, map[string]interface{}{
				"claim_index":            ci,
				"claim":                  claim,
				"decision":               decision,
				"confidence_zone":        zone,
				"calibrated_confidence":  conf.Float64,
				"failure_type":           ft,
				"evidence":               parseJSONField(ev.String),
				"span_attribution":       parseJSONField(sa.String),
				"evaluation":             parseJSONField(evl.String),
			})
		}
	}

	return map[string]interface{}{
		"report_id":     r.ID,
		"status":        r.Status,
		"claim_count":   r.ClaimCount,
		"summary":       r.Summary,
		// share_token intentionally omitted: exposed only via /r/{token}.
		"claims": claims,
		"created_at": r.CreatedAt,
	}, nil
}

func parseJSONField(s string) interface{} {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	return v
}
func jsonMarshal(v interface{}) (string, error) {
	if v == nil {
		return "null", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "null", err
	}
	return string(b), nil
}
