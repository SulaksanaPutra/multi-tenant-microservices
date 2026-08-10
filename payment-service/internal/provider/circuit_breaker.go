package provider

import (
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

type State string

const (
	StateClosed   State = "CLOSED"
	StateOpen     State = "OPEN"
	StateHalfOpen State = "HALF_OPEN"
)

type CircuitBreaker struct {
	mu           sync.RWMutex
	state        State
	failures     int
	maxFailures  int
	cooldown     time.Duration
	lastStateChange time.Time
}

func NewCircuitBreaker(maxFailures int, cooldown time.Duration) *CircuitBreaker {
	if maxFailures <= 0 {
		maxFailures = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &CircuitBreaker{
		state:           StateClosed,
		maxFailures:     maxFailures,
		cooldown:        cooldown,
		lastStateChange: time.Now(),
	}
}

func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		if now.Sub(cb.lastStateChange) > cb.cooldown {
			cb.state = StateHalfOpen
			cb.lastStateChange = now
			return true
		}
		return false
	case StateHalfOpen:
		return true
	default:
		return true
	}
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = StateClosed
	cb.lastStateChange = time.Now()
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastStateChange = time.Now()

	if cb.failures >= cb.maxFailures {
		cb.state = StateOpen
	}
}

func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return cb.state
}
