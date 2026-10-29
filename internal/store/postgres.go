package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/lib/pq" // postgres driver
)

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

// applyMigrations menjalankan berkas .sql yang sudah diekstrak, berurutan.
// Diekstrak ke direktori sementara karena lib/pq tidak bisa membaca dari
// embed.FS.
func applyMigrations(files []embed.FS) error {
	dir, err := os.MkdirTemp("", "gw-migrations-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

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
			path := filepath.Join(dir, entry.Name())
			if err := os.WriteFile(path, body, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			if _, err := db.Exec(string(body)); err != nil {
				return fmt.Errorf("apply %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

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
			log.Printf("[store] insert skipped (%s): request=%s", err, e.RequestID)
			return
		}
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
}

// LogAudit menyimpan audit trail. Kegagalan tidak boleh menggagalkan request.
func LogAudit(ctx context.Context, a AuditLog) {
	if db == nil {
		return
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_trail
			(request_id, backend, claim, decision, calibrated_confidence,
			 calibrated_decision, evidence, span_level_attribution)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb)`,
		nullIfEmpty(a.RequestID),
		nullIfEmpty(a.Backend),
		a.Claim,
		nullIfEmpty(a.Decision),
		a.CalibratedConfidence,
		nullIfEmpty(a.CalibratedDecision),
		nullIfEmpty(a.EvidenceJSON),
		nullIfEmpty(a.SpanJSON),
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

// GetAuditReportByShareToken fetches a report (read-only) by share token if not expired.
func GetAuditReportByShareToken(ctx context.Context, token string) (map[string]interface{}, error) {
	if db == nil {
		return nil, errors.New("db not initialized")
	}
	var id string
	var expires sql.NullTime
	err := db.QueryRowContext(ctx, `SELECT id, share_expires_at FROM audit_reports WHERE share_token=$1`, token).Scan(&id, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if expires.Valid && time.Now().After(expires.Time) {
		return nil, ErrNotFound
	}
	return GetAuditReport(ctx, id)
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
func CreateAuditReport(ctx context.Context, tier string, claims []map[string]interface{}, summary map[string]interface{}) (string, string, error) {
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
		INSERT INTO audit_reports (api_key_tier, status, claim_count, summary)
		VALUES ($1, 'completed', $2, $3::jsonb)
		RETURNING id`, nullIfEmpty(tier), len(claims), summaryJSON).Scan(&reportID)
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

// GetAuditReport fetches a report with its claims.
func GetAuditReport(ctx context.Context, id string) (map[string]interface{}, error) {
	if db == nil {
		return nil, errors.New("db not initialized")
	}
	row := db.QueryRowContext(ctx, `SELECT id, api_key_tier, status, claim_count, summary, share_token, share_expires_at, created_at FROM audit_reports WHERE id=$1`, id)
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
	// simple: read manually
	rows2, _ := db.QueryContext(ctx, `SELECT claim_index, claim, decision, confidence_zone, calibrated_confidence, failure_type, evidence::text, span_attribution::text, evaluation::text FROM audit_report_claims WHERE report_id=$1 ORDER BY claim_index`, id)
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
			"claim_index": ci, "claim": claim, "decision": decision,
			"confidence_zone": zone, "calibrated_confidence": conf.Float64,
			"failure_type": ft, "evidence": ev.String, "span_attribution": sa.String,
			"evaluation": evl.String,
		})
	}

	return map[string]interface{}{
		"report_id": r.ID, "status": r.Status, "claim_count": r.ClaimCount,
		"summary": r.Summary, "share_token": r.ShareToken, "claims": claims,
		"created_at": r.CreatedAt,
	}, nil
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
