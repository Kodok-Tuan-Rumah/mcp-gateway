package main

import (
	"context"
	"testing"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/store"
)

func TestLogEvalAsync_ContextNotCancelledByParent(t *testing.T) {
	reqCtx, cancelReq := context.WithCancel(context.Background())

	observed := make(chan error, 1)
	write := func(ctx context.Context, _ store.EvalLog) {
		time.Sleep(50 * time.Millisecond)
		observed <- ctx.Err()
	}

	logEvalAsync(reqCtx, store.EvalLog{Query: "q"}, write)
	cancelReq()

	select {
	case err := <-observed:
		if err != nil {
			t.Fatalf("context penulisan log dibatalkan oleh parent: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write tidak pernah dipanggil (timeout)")
	}
}

// Pastikan penulisan tetap punya batas waktu sendiri, jadi request yang
func TestLogEvalAsync_HasOwnDeadline(t *testing.T) {
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()

	deadline := make(chan bool, 1)
	write := func(ctx context.Context, _ store.EvalLog) {
		_, ok := ctx.Deadline()
		deadline <- ok
	}

	logEvalAsync(reqCtx, store.EvalLog{Query: "q"}, write)

	select {
	case ok := <-deadline:
		if !ok {
			t.Error("context penulisan log tidak punya deadline")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write tidak pernah dipanggil (timeout)")
	}
}

// Data yang diteruskan harus utuh — regression guard kalau refactor
func TestLogEvalAsync_PassesEvalLogIntact(t *testing.T) {
	score := 0.86
	in := store.EvalLog{
		RequestID:        "req-1",
		Query:            "apa itu RAG?",
		Answer:           "Retrieval Augmented Generation",
		Backend:          "arxiv",
		BackendModel:     "openai/gpt-oss-20b",
		Orchestrator:     "rule-based",
		Decision:         "answer",
		FailureType:      "none",
		EvaluationStatus: "completed",
		Faithfulness:     &score,
		FailureMode:      "none",
		DurationMs:       1234,
	}

	got := make(chan store.EvalLog, 1)
	write := func(_ context.Context, e store.EvalLog) { got <- e }

	logEvalAsync(context.Background(), in, write)

	select {
	case out := <-got:
		if out != in {
			t.Errorf("EvalLog berubah: dapat %+v, mau %+v", out, in)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write tidak pernah dipanggil (timeout)")
	}
}
