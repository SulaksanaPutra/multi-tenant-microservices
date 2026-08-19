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

	cb.RecordFailure()
	cb.RecordFailure()
	if !cb.Allow() || cb.State() != StateClosed {
		t.Fatalf("expected CLOSED state after 2 failures, got %s", cb.State())
	}

	cb.RecordFailure()
	if cb.Allow() || cb.State() != StateOpen {
		t.Fatalf("expected OPEN state after 3 failures, got %s", cb.State())
	}

	time.Sleep(150 * time.Millisecond)

	if !cb.Allow() {
		t.Fatalf("expected Allow() to return true in HALF_OPEN state")
	}

	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Fatalf("expected CLOSED state after success, got %s", cb.State())
	}
}
