package repository

import (
	"testing"

	"payment-service/internal/infrastructure/postgres"
)

func TestPaymentRepository_Constructor(t *testing.T) {
	paymentRepository := NewPaymentRepository(&postgres.Client{})
	if paymentRepository == nil {
		t.Fatal("expected NewPaymentRepository to return non-nil struct pointer")
	}
}

func TestOutboxRepository_Constructor(t *testing.T) {
	outboxRepository := NewOutboxRepository(&postgres.Client{})
	if outboxRepository == nil {
		t.Fatal("expected NewOutboxRepository to return non-nil struct pointer")
	}
}

func TestInboxRepository_Constructor(t *testing.T) {
	inboxRepository := NewInboxRepository(&postgres.Client{})
	if inboxRepository == nil {
		t.Fatal("expected NewInboxRepository to return non-nil struct pointer")
	}
}

func TestPSPConfigRepository_Constructor(t *testing.T) {
	pspConfigRepository := NewPSPConfigRepository(&postgres.Client{})
	if pspConfigRepository == nil {
		t.Fatal("expected NewPSPConfigRepository to return non-nil struct pointer")
	}
}
