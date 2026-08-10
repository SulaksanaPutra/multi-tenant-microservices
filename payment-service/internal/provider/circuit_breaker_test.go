package provider

import (
	"testing"
	"time"
)

func TestCircuitBreaker_TripsAndResets(t *testing.T) {
	cb := NewCircuitBreaker(3, 100*time.Millisecond)

	if cb.State() != StateClosed {
		t.Fatalf("expected initial state CLOSED, got %s", cb.State())
	}

	// 2 failures should keep circuit CLOSED
	cb.RecordFailure()
	cb.RecordFailure()
	if !cb.Allow() || cb.State() != StateClosed {
		t.Fatalf("expected CLOSED state after 2 failures, got %s", cb.State())
	}

	// 3rd failure should trip circuit to OPEN
	cb.RecordFailure()
	if cb.Allow() || cb.State() != StateOpen {
		t.Fatalf("expected OPEN state after 3 failures, got %s", cb.State())
	}

	// Wait for cooldown
	time.Sleep(150 * time.Millisecond)

	// Circuit should transition to HALF_OPEN on next Allow() call
	if !cb.Allow() {
		t.Fatalf("expected Allow() to return true in HALF_OPEN state")
	}

	// Record success should reset circuit to CLOSED
	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Fatalf("expected CLOSED state after success, got %s", cb.State())
	}
}
