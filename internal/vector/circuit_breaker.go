package vector

import (
	"log/slog"
	"sync"
	"time"
)

// CircuitState represents the state of the circuit breaker.
type CircuitState int32

const (
	StateClosed   CircuitState = iota // normal operation
	StateOpen                         // failing, reject requests with backoff
	StateHalfOpen                     // probing single request
	StateDisabled                     // kept for API compat; functionally same as StateOpen
)

// CircuitBreaker protects Ollama calls with a state machine (IP-15: mutex-based).
// Uses exponential backoff instead of permanent disable: cooldown doubles on each
// open cycle up to backoffMax, then keeps retrying at backoffMax intervals.
type CircuitBreaker struct {
	mu              sync.Mutex
	state           CircuitState
	consecutiveFail int
	openCycles      int
	lastOpenTime    time.Time
	baseCooldown    time.Duration
	currentCooldown time.Duration
	backoffMax      time.Duration
	failThreshold   int
	halfOpenProbing bool // true when a probe request is in flight
}

// NewCircuitBreaker creates a circuit breaker with defaults.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:           StateClosed,
		baseCooldown:    5 * time.Minute,
		currentCooldown: 5 * time.Minute,
		backoffMax:      1 * time.Hour,
		failThreshold:   3,
	}
}

// Allow returns true if a request should proceed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if time.Since(cb.lastOpenTime) > cb.currentCooldown {
			cb.state = StateHalfOpen
			cb.halfOpenProbing = true
			return true
		}
		return false
	case StateHalfOpen:
		if cb.halfOpenProbing {
			return false // only one probe at a time
		}
		cb.halfOpenProbing = true
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful call.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	prevState := cb.state
	cb.consecutiveFail = 0
	cb.openCycles = 0
	cb.currentCooldown = cb.baseCooldown
	cb.halfOpenProbing = false
	cb.state = StateClosed
	cb.mu.Unlock()

	if prevState != StateClosed {
		slog.Info("circuit breaker recovered",
			"from", stateString(prevState), "to", "closed")
	}
}

// RecordFailure records a failed call. Transitions to OPEN with exponential backoff.
// In HalfOpen, a single failure immediately re-opens the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.halfOpenProbing = false

	// HalfOpen probe failed → immediately re-open with escalated backoff
	if cb.state == StateHalfOpen {
		cb.openCycles++
		cb.state = StateOpen
		cb.lastOpenTime = time.Now()
		cb.currentCooldown = min(cb.currentCooldown*2, cb.backoffMax)
		slog.Warn("circuit breaker opened",
			"from", "half_open", "cooldown", cb.currentCooldown, "cycles", cb.openCycles)
		return
	}

	cb.consecutiveFail++
	if cb.consecutiveFail >= cb.failThreshold {
		prevState := cb.state
		cb.openCycles++
		cb.state = StateOpen
		cb.lastOpenTime = time.Now()
		cb.consecutiveFail = 0
		// Exponential backoff: 5m → 10m → 20m → 40m → 60m (capped)
		cb.currentCooldown = min(cb.currentCooldown*2, cb.backoffMax)
		slog.Warn("circuit breaker opened",
			"from", stateString(prevState), "cooldown", cb.currentCooldown, "cycles", cb.openCycles)
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Reset forces the circuit breaker back to CLOSED.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.consecutiveFail = 0
	cb.openCycles = 0
	cb.currentCooldown = cb.baseCooldown
	cb.halfOpenProbing = false
}

func stateString(s CircuitState) string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}
