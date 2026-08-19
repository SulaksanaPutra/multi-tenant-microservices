package repository

import (
	"testing"

	"payment-service/internal/infrastructure/postgres"
)

func TestDebtRepository_New(t *testing.T) {
	debtRepository := NewDebtRepository(&postgres.Client{})
	if debtRepository == nil {
		t.Fatal("expected non-nil DebtRepository")
	}
}
