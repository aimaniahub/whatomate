package lock

import (
	"sync"
	"time"
)

// CircuitBreaker is a simple trip switch for external deps (Phase 17).
type CircuitBreaker struct {
	mu          sync.Mutex
	failures    int
	threshold   int
	openUntil   time.Time
	cooldown    time.Duration
}

// NewCircuitBreaker trips after threshold consecutive failures, then cools down.
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &CircuitBreaker{threshold: threshold, cooldown: cooldown}
}

// Allow reports whether a call may proceed.
func (c *CircuitBreaker) Allow() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.openUntil) {
		return false
	}
	return true
}

// Success resets the failure counter.
func (c *CircuitBreaker) Success() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.failures = 0
	c.openUntil = time.Time{}
	c.mu.Unlock()
}

// Failure records a failure and may open the circuit.
func (c *CircuitBreaker) Failure() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= c.threshold {
		c.openUntil = time.Now().Add(c.cooldown)
		c.failures = 0
	}
}
