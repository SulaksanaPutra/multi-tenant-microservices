package repository

import (
	"database/sql"
	"testing"
)

func TestPaymentRepository_Constructor(t *testing.T) {
	repo := NewPaymentRepository(&sql.DB{})
	if repo == nil {
		t.Fatal("expected NewPaymentRepository to return non-nil struct pointer")
	}
}

func TestOutboxRepository_Constructor(t *testing.T) {
	repo := NewOutboxRepository(&sql.DB{})
	if repo == nil {
		t.Fatal("expected NewOutboxRepository to return non-nil struct pointer")
	}
}

func TestInboxRepository_Constructor(t *testing.T) {
	repo := NewInboxRepository(&sql.DB{})
	if repo == nil {
		t.Fatal("expected NewInboxRepository to return non-nil struct pointer")
	}
}

func TestPSPConfigRepository_Constructor(t *testing.T) {
	repo := NewPSPConfigRepository(&sql.DB{})
	if repo == nil {
		t.Fatal("expected NewPSPConfigRepository to return non-nil struct pointer")
	}
}
