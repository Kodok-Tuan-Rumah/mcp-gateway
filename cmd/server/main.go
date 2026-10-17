// Package main adalah entrypoint gateway.
//
// Seluruh wiring HTTP ada di router.go, pemetaan ke kontrak publik di
// respond.go, dan penyusunan plan di plan.go — supaya semuanya bisa diuji
// tanpa menjalankan proses server.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/config"
	"github.com/malikimayzar/mcp-gateway/internal/registry"
	"github.com/malikimayzar/mcp-gateway/internal/store"
	"github.com/malikimayzar/mcp-gateway/internal/tools"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("invalid runtime configuration: %v", err)
	}

	store.Init(cfg.PostgresDSN)
	defer store.Close()

	reg := newRegistry(cfg)

	log.Printf("MCP Gateway starting on %s", cfg.ListenAddress)
	if err := http.ListenAndServe(cfg.ListenAddress, newRouterWithConfig(reg, cfg)); err != nil {
		log.Fatal(err)
	}
}

// newRegistry mendaftarkan tool yang tersedia.
//
// retrieve_chunks sengaja tetap terdaftar supaya endpoint /tool bisa
// dipanggil dan errornya terlihat apa adanya, tapi tool ini TIDAK dipakai
// jalur jawaban mana pun pada v1 — lihat backendTools di plan.go.
func newRegistry(cfg config.Config) *registry.Registry {
	reg := registry.New()
	reg.Register("search_arxiv", tools.NewSearchArxiv(cfg.Arxiv))
	reg.Register("research_rag", tools.NewResearchRAG(cfg.RAG))
	reg.Register("retrieve_chunks", tools.NewRetrieveChunks(cfg.RAG))
	reg.Register("evaluate_answer", tools.NewEvaluateAnswer(cfg.Evaluator))
	return reg
}

// evalWriter adalah fungsi penulisan log evaluasi (store.LogEval), di-inject
// supaya logEvalAsync bisa diuji tanpa PostgreSQL.
type evalWriter func(ctx context.Context, e store.EvalLog)

// logEvalAsync menulis hasil evaluasi ke PostgreSQL di luar jalur serving.
//
// Context permintaan asli sudah dibatalkan begitu handler selesai (defer cancel),
// sehingga goroutine yang memakai context itu akan menerima "context canceled"
// dan INSERT-nya gagal. Supaya penulisan tetap terjadi, context di-"detach"
// dari pembatalan parent, lalu diberi batas waktu sendiri.
//
// Catatan: context harus dibuat DI DALAM goroutine. Kalau dibuat di luar,
// cancel()-nya akan ikut ter-defer oleh handler dan membatalkan ulang
// penulisan yang sedang berjalan.
func logEvalAsync(ctx context.Context, e store.EvalLog, write evalWriter) {
	logEvalAsyncWithTimeout(ctx, e, config.Default().StoreLogTimeout, write)
}

// logEvalAsyncWithTimeout is the production variant; its timeout comes from
// startup configuration rather than a package constant.
func logEvalAsyncWithTimeout(ctx context.Context, e store.EvalLog, timeout time.Duration, write evalWriter) {
	go func() {
		logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		write(logCtx, e)
	}()
}
