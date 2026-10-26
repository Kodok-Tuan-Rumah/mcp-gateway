// Package circuitbreaker implements the circuit breaker pattern for
// downstream service calls.
//
// States:
//   - Closed: requests pass through normally. Failures are counted.
//   - Open: requests are rejected immediately. After a timeout, transitions to half-open.
//   - Half-Open: a single probe request is allowed. Success → closed, failure → open.
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the current state of the circuit breaker.
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned when the circuit is open and requests are rejected.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// Config holds circuit breaker configuration.
type Config struct {
	// MaxFailures is the number of consecutive failures before opening the circuit.
	MaxFailures int
	// Timeout is how long the circuit stays open before transitioning to half-open.
	Timeout time.Duration
	// MaxRequests is the number of probe requests allowed in half-open state.
	MaxRequests int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxFailures: 5,
		Timeout:     30 * time.Second,
		MaxRequests: 1,
	}
}

// Breaker implements the circuit breaker pattern.
type Breaker struct {
	mu          sync.Mutex
	name        string
	config      Config
	state       State
	failures    int
	lastFailure time.Time
	halfOpen    int // number of probe requests in half-open state
}

// New creates a new circuit breaker with the given name and config.
func New(name string, cfg Config) *Breaker {
	return &Breaker{
		name:   name,
		config: cfg,
		state:  StateClosed,
	}
}

// Name returns the breaker's name.
func (b *Breaker) Name() string {
	return b.name
}

// State returns the current state.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updateState()
	return b.state
}

// Execute runs the given function if the circuit allows it.
// Returns ErrCircuitOpen if the circuit is open.
func (b *Breaker) Execute(fn func() error) error {
	b.mu.Lock()
	b.updateState()

	if b.state == StateOpen {
		b.mu.Unlock()
		return ErrCircuitOpen
	}

	if b.state == StateHalfOpen {
		if b.halfOpen >= b.config.MaxRequests {
			b.mu.Unlock()
			return ErrCircuitOpen
		}
		b.halfOpen++
	}
	b.mu.Unlock()

	// Execute the function
	err := fn()

	b.mu.Lock()
	defer b.mu.Unlock()

	if err != nil {
		b.failures++
		b.lastFailure = time.Now()
		if b.state == StateHalfOpen {
			// Probe failed → reopen
			b.state = StateOpen
			b.halfOpen = 0
		} else if b.failures >= b.config.MaxFailures {
			b.state = StateOpen
		}
		return err
	}

	// Success
	if b.state == StateHalfOpen {
		// Probe succeeded → close
		b.state = StateClosed
		b.failures = 0
		b.halfOpen = 0
	} else {
		b.failures = 0
	}
	return nil
}

// updateState transitions open → half-open if timeout has elapsed.
// Must be called with lock held.
func (b *Breaker) updateState() {
	if b.state == StateOpen && time.Since(b.lastFailure) >= b.config.Timeout {
		b.state = StateHalfOpen
		b.halfOpen = 0
	}
}

// Allow checks if a request can proceed without executing a function.
// Returns ErrCircuitOpen if the circuit is open.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updateState()

	if b.state == StateOpen {
		return ErrCircuitOpen
	}
	if b.state == StateHalfOpen {
		if b.halfOpen >= b.config.MaxRequests {
			return ErrCircuitOpen
		}
		b.halfOpen++
	}
	return nil
}

// RecordSuccess records a successful request.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateHalfOpen {
		b.state = StateClosed
		b.failures = 0
		b.halfOpen = 0
	} else {
		b.failures = 0
	}
}

// RecordFailure records a failed request.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.lastFailure = time.Now()
	if b.state == StateHalfOpen {
		b.state = StateOpen
		b.halfOpen = 0
	} else if b.failures >= b.config.MaxFailures {
		b.state = StateOpen
	}
}

// Reset forces the circuit to closed state.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = StateClosed
	b.failures = 0
	b.halfOpen = 0
}

// Failures returns the current failure count.
func (b *Breaker) Failures() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.failures
}

// Manager holds circuit breakers for multiple services.
type Manager struct {
	mu       sync.RWMutex
	breakers map[string]*Breaker
	config   Config
}

// NewManager creates a new circuit breaker manager.
func NewManager(cfg Config) *Manager {
	return &Manager{
		breakers: make(map[string]*Breaker),
		config:  cfg,
	}
}

// Get returns the circuit breaker for the given service name,
// creating one if it doesn't exist.
func (m *Manager) Get(name string) *Breaker {
	m.mu.RLock()
	b, ok := m.breakers[name]
	m.mu.RUnlock()
	if ok {
		return b
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.breakers[name]; ok {
		return b
	}
	b = New(name, m.config)
	m.breakers[name] = b
	return b
}

// ResetAll resets all circuit breakers to closed state.
func (m *Manager) ResetAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.breakers {
		b.Reset()
	}
}

// States returns a map of service name → state string.
func (m *Manager) States() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	states := make(map[string]string, len(m.breakers))
	for name, b := range m.breakers {
		states[name] = b.State().String()
	}
	return states
}