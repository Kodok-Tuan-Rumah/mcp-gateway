package main

import (
	"context"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/store"
)

// logEvalAsync menjalankan penulisan log evaluasi di goroutine terpisah,
// dengan context baru yang tidak dibatalkan oleh parent request context.
// Ini mencegah "context canceled" error saat handler selesai sebelum DB write selesai.
func logEvalAsync(ctx context.Context, e store.EvalLog, write func(context.Context, store.EvalLog)) {
	// Buat context baru dengan timeout 5 detik, tidak terikat dengan parent.
	childCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer cancel()
		write(childCtx, e)
	}()
}

// logEvalAsyncWithTimeout menjalankan penulisan log evaluasi di goroutine terpisah,
// dengan context baru yang memiliki deadline sendiri.
func logEvalAsyncWithTimeout(ctx context.Context, e store.EvalLog, timeout time.Duration, write func(context.Context, store.EvalLog)) {
	childCtx, cancel := context.WithTimeout(context.Background(), timeout)
	go func() {
		defer cancel()
		write(childCtx, e)
	}()
}