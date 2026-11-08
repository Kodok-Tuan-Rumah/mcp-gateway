package main

import (
	"context"
	"sync"
	"time"

	"github.com/malikimayzar/mcp-gateway/internal/store"
)

// auditJob is a unit of audit/eval log work to be written durably.
type auditJob struct {
	ctx context.Context
	e   store.EvalLog
}

// AuditWriter is a bounded, worker-pool based audit log writer.
//
// It guarantees that every enqueued EvalLog is eventually written (unless the
// channel is full, in which case the oldest job is dropped to avoid blocking
// the request path). On shutdown, Drain() waits for all queued and in-flight
// jobs to complete before the process exits.
type AuditWriter struct {
	jobs   chan auditJob
	wg     sync.WaitGroup
	worker sync.WaitGroup
	stop   chan struct{}
	once   sync.Once
}

// NewAuditWriter creates an AuditWriter with the given buffer size and number
// of worker goroutines.
func NewAuditWriter(bufferSize int, numWorkers int) *AuditWriter {
	a := &AuditWriter{
		jobs:  make(chan auditJob, bufferSize),
		stop:  make(chan struct{}),
	}
	a.worker.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go a.workerLoop()
	}
	return a
}

// workerLoop reads jobs from the channel and writes them via store.LogEval.
func (a *AuditWriter) workerLoop() {
	defer a.worker.Done()
	for {
		select {
		case job, ok := <-a.jobs:
			if !ok {
				return // channel closed
			}
			store.LogEval(job.ctx, job.e)
			a.wg.Done()
		case <-a.stop:
			return
		}
	}
}

// Enqueue queues an EvalLog for durable writing. It never blocks the request
// path: if the channel is full, the job is dropped and a failure is counted
// by store.LogEval's prometheus metric (audit_write_failures_total).
//
// The WaitGroup is incremented before enqueuing so that Drain() can wait for
// all jobs, including those still sitting in the channel.
func (a *AuditWriter) Enqueue(ctx context.Context, e store.EvalLog) {
	a.wg.Add(1)
	select {
	case a.jobs <- auditJob{ctx: ctx, e: e}:
	default:
		// Channel full — drop to avoid blocking the request path.
		a.wg.Done()
	}
}

// Drain waits for all queued and in-flight audit writes to complete. Call this
// during graceful shutdown before closing the process.
func (a *AuditWriter) Drain() {
	// Wait for the channel to empty and all in-flight jobs to finish.
	a.wg.Wait()
}

// Close stops the worker goroutines. Must be called after Drain().
func (a *AuditWriter) Close() {
	a.once.Do(func() {
		close(a.stop)
	})
}

// globalAuditWriter is the process-global audit writer, wired into main.go.
var globalAuditWriter *AuditWriter

// logEvalAsync enqueues an EvalLog for durable async writing via the global
// audit writer. It never blocks the request path.
func logEvalAsync(ctx context.Context, e store.EvalLog, write func(context.Context, store.EvalLog)) {
	childCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if globalAuditWriter != nil {
		globalAuditWriter.Enqueue(childCtx, e)
		return
	}
	// Fallback: direct synchronous write if writer not initialized.
	write(childCtx, e)
	cancel()
}

// logEvalAsyncWithTimeout enqueues with a per-job context timeout.
func logEvalAsyncWithTimeout(ctx context.Context, e store.EvalLog, timeout time.Duration, write func(context.Context, store.EvalLog)) {
	childCtx, cancel := context.WithTimeout(context.Background(), timeout)
	if globalAuditWriter != nil {
		globalAuditWriter.Enqueue(childCtx, e)
		return
	}
	write(childCtx, e)
	cancel()
}
