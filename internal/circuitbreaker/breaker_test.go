package circuitbreaker

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBreakerClosedState(t *testing.T) {
	b := New("test", DefaultConfig())

	if b.State() != StateClosed {
		t.Errorf("expected closed, got %s", b.State())
	}

	// Should allow requests in closed state
	if err := b.Allow(); err != nil {
		t.Errorf("expected allow in closed state, got %v", err)
	}

	// Record failures
	for i := 0; i < 4; i++ {
		b.RecordFailure()
	}
	if b.State() != StateClosed {
		t.Errorf("expected closed after 4 failures (max=5), got %s", b.State())
	}

	// 5th failure should open
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Errorf("expected open after 5 failures, got %s", b.State())
	}
}

func TestBreakerOpenState(t *testing.T) {
	b := New("test", Config{MaxFailures: 2, Timeout: 100 * time.Millisecond, MaxRequests: 1})

	// Open the circuit
	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("expected open, got %s", b.State())
	}

	// Should reject requests
	if err := b.Allow(); err != ErrCircuitOpen {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}

	// After timeout, should transition to half-open
	time.Sleep(150 * time.Millisecond)
	if b.State() != StateHalfOpen {
		t.Errorf("expected half-open after timeout, got %s", b.State())
	}
}

func TestBreakerHalfOpenState(t *testing.T) {
	b := New("test", Config{MaxFailures: 2, Timeout: 50 * time.Millisecond, MaxRequests: 1})

	// Open the circuit
	b.RecordFailure()
	b.RecordFailure()

	// Wait for half-open
	time.Sleep(100 * time.Millisecond)
	if b.State() != StateHalfOpen {
		t.Fatalf("expected half-open, got %s", b.State())
	}

	// Allow probe request
	if err := b.Allow(); err != nil {
		t.Errorf("expected allow probe in half-open, got %v", err)
	}

	// Second probe should be rejected
	if err := b.Allow(); err != ErrCircuitOpen {
		t.Errorf("expected reject second probe, got %v", err)
	}

	// Record success → should close
	b.RecordSuccess()
	if b.State() != StateClosed {
		t.Errorf("expected closed after success probe, got %s", b.State())
	}
}

func TestBreakerHalfOpenFailure(t *testing.T) {
	b := New("test", Config{MaxFailures: 2, Timeout: 50 * time.Millisecond, MaxRequests: 1})

	// Open the circuit
	b.RecordFailure()
	b.RecordFailure()

	// Wait for half-open
	time.Sleep(100 * time.Millisecond)

	// Allow and fail probe
	b.Allow()
	b.RecordFailure()

	// Should reopen
	if b.State() != StateOpen {
		t.Errorf("expected open after failed probe, got %s", b.State())
	}
}

func TestBreakerExecute(t *testing.T) {
	b := New("test", DefaultConfig())

	// Success case
	err := b.Execute(func() error { return nil })
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if b.Failures() != 0 {
		t.Errorf("expected 0 failures, got %d", b.Failures())
	}

	// Failure case
	testErr := errors.New("test error")
	err = b.Execute(func() error { return testErr })
	if err != testErr {
		t.Errorf("expected testErr, got %v", err)
	}
	if b.Failures() != 1 {
		t.Errorf("expected 1 failure, got %d", b.Failures())
	}
}

func TestBreakerReset(t *testing.T) {
	b := New("test", Config{MaxFailures: 2, Timeout: 1 * time.Hour, MaxRequests: 1})

	b.RecordFailure()
	b.RecordFailure()
	if b.State() != StateOpen {
		t.Fatalf("expected open, got %s", b.State())
	}

	b.Reset()
	if b.State() != StateClosed {
		t.Errorf("expected closed after reset, got %s", b.State())
	}
	if b.Failures() != 0 {
		t.Errorf("expected 0 failures after reset, got %d", b.Failures())
	}
}

func TestBreakerManager(t *testing.T) {
	m := NewManager(DefaultConfig())

	b1 := m.Get("service1")
	b2 := m.Get("service1")
	if b1 != b2 {
		t.Error("expected same breaker for same name")
	}

	b3 := m.Get("service2")
	if b3 == b1 {
		t.Error("expected different breaker for different name")
	}

	states := m.States()
	if len(states) != 2 {
		t.Errorf("expected 2 states, got %d", len(states))
	}

	m.ResetAll()
	if b1.State() != StateClosed {
		t.Error("expected closed after ResetAll")
	}
}

func TestBreakerConcurrent(t *testing.T) {
	b := New("test", DefaultConfig())
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Allow()
			if i%2 == 0 {
				b.RecordSuccess()
			} else {
				b.RecordFailure()
			}
		}()
	}
	wg.Wait()

	// Should not panic or deadlock
	_ = b.State()
}