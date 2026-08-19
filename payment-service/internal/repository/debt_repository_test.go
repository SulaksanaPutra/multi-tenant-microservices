package repository

import (
	"testing"

	"payment-service/internal/infrastructure/postgres"
)

func TestDebtRepository_New(t *testing.T) {
	repo := NewDebtRepository(&postgres.Client{})
	if repo == nil {
		t.Fatal("expected non-nil DebtRepository")
	}
}
