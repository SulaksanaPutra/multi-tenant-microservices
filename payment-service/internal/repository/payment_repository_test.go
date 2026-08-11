package repository

import (
	"testing"

	"payment-service/internal/infrastructure/postgres"
)

func TestPaymentRepository_Constructor(t *testing.T) {
	repo := NewPaymentRepository(&postgres.Client{})
	if repo == nil {
		t.Fatal("expected NewPaymentRepository to return non-nil struct pointer")
	}
}

func TestOutboxRepository_Constructor(t *testing.T) {
	repo := NewOutboxRepository(&postgres.Client{})
	if repo == nil {
		t.Fatal("expected NewOutboxRepository to return non-nil struct pointer")
	}
}

func TestInboxRepository_Constructor(t *testing.T) {
	repo := NewInboxRepository(&postgres.Client{})
	if repo == nil {
		t.Fatal("expected NewInboxRepository to return non-nil struct pointer")
	}
}

func TestPSPConfigRepository_Constructor(t *testing.T) {
	repo := NewPSPConfigRepository(&postgres.Client{})
	if repo == nil {
		t.Fatal("expected NewPSPConfigRepository to return non-nil struct pointer")
	}
}
