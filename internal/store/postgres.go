package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/lib/pq" // postgres driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DefaultPostgresDSN adalah nilai fallback untuk pengembangan lokal.
const DefaultPostgresDSN = "host=localhost port=5432 user=arxiv password=arxiv_secret dbname=arxiv_db sslmode=disable"

// LogTable adalah tabel milik gateway.
//
// Tabel `query_logs` milik arxiv-research-assistant dan tidak dipakai di sini:
// go-backend mengisi model sebagai nama LLM dan retrieval_ms sebagai latensi
// milestone backend, sedangkan gateway butuh orchestrator, status evaluasi, dan
// latensi total. Satu kolom dengan dua definisi membuat hasil analitis tidak
// bisa dipercaya.
const LogTable = "gateway_query_logs"

var db *sql.DB

// Init membuka koneksi PostgreSQL.
//
// dsn diberikan pemanggil (diambil dari config saat startup) supaya store tidak
// membaca environment variable sendiri dan supaya nilai yang dipakai bisa
// diuji. Kalau dsn kosong, konfigurasi default lokal dipakai.
func Init(dsn string) {
	if dsn == "" {
		dsn = DefaultPostgresDSN
	}

	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		log.Printf("[store] failed to open DB: %v", err)
		return
	}

	if err := db.Ping(); err != nil {
		log.Printf("[store] failed to ping DB: %v", err)
		db = nil
		return
	}

	if err := migrate(); err != nil {
		// Skema yang tidak ada akan membuat setiap penulisan gagal, jadi lebih
		// baik dinonaktifkan sekarang dan dilaporkan.
		log.Printf("[store] migration failed, logging disabled: %v", err)
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

func Close() {
	if db != nil {
		_ = db.Close()
	}
}
