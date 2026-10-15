package store

import (
	"context"
	"database/sql"
	"log"
	"time"

	_ "github.com/lib/pq" // postgres driver
)

// DefaultPostgresDSN adalah nilai fallback untuk pengembangan lokal.
const DefaultPostgresDSN = "host=localhost port=5432 user=arxiv password=arxiv_secret dbname=arxiv_db sslmode=disable"

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

	log.Println("[store] connected to PostgreSQL")
}

type EvalLog struct {
	Query             string
	Answer            string
	Faithfulness      float64
	FailureMode       string
	RetrievalStrategy string
	Orchestrator      string
	Retried           bool
	DurationMs        int
}

func LogEval(ctx context.Context, e EvalLog) {
	if db == nil {
		return
	}

	// model field kita pakai buat simpan orchestrator (groq/rule-based)
	_, err := db.ExecContext(ctx, `
		INSERT INTO query_logs
			(query, answer, faithfulness, failure_mode, retrieval_strategy, model, retrieval_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.Query,
		e.Answer,
		e.Faithfulness,
		e.FailureMode,
		e.RetrievalStrategy,
		e.Orchestrator,
		e.DurationMs,
	)
	if err != nil {
		log.Printf("[store] insert failed: %v", err)
		return
	}
	log.Printf("[store] eval logged | query=%q score=%.4f orchestrator=%s", e.Query, e.Faithfulness, e.Orchestrator)
}

func Close() {
	if db != nil {
		_ = db.Close()
	}
}

// extractFailureMode ambil failure_mode dari eval result kalau ada
func ExtractFailureMode(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	if fm, ok := data["failure_mode"].(string); ok {
		return fm
	}
	return ""
}

// dummy agar time import tidak unused
var _ = time.Second
