package service

import (
	"context"
	"testing"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
)

func TestDebtService_CreatePayableDebt(t *testing.T) {
	var createdInput repository.CreateDebtInput
	debtRepository := &mockDebtRepository{
		findByOrderIDFn: func(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error) {
			return nil, domain.ErrDebtNotFound
		},
		createFn: func(ctx context.Context, input repository.CreateDebtInput) error {
			createdInput = input
			return nil
		},
	}
	debtService := NewDebtService(nil, debtRepository, nil)

	debt, err := debtService.CreatePayableDebt(context.Background(), CreatePayableDebtInput{
		TenantID: "tnt_1",
		OrderID:  "ord_1",
		Amount:   250.0,
		Currency: "USD",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if debt.TenantID != "tnt_1" || debt.OrderID != "ord_1" || debt.TotalAmount != 250.0 {
		t.Errorf("unexpected debt output: %+v", debt)
	}
	if createdInput.Status != domain.DebtStatusUnpaid {
		t.Errorf("expected UNPAID status, got %s", createdInput.Status)
	}
}

func TestDebtService_GetPayableDebtByOrderID(t *testing.T) {
	debtRepository := &mockDebtRepository{
		findByOrderIDFn: func(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error) {
			return &domain.PayableDebt{
				ID:          "debt_123",
				TenantID:    tenantID,
				OrderID:     orderID,
				TotalAmount: 100.0,
				PaidAmount:  0.0,
				Currency:    "USD",
				Status:      domain.DebtStatusUnpaid,
			}, nil
		},
	}
	debtService := NewDebtService(nil, debtRepository, nil)

	debt, err := debtService.GetPayableDebtByOrderID(context.Background(), "tnt_1", "ord_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if debt.ID != "debt_123" {
		t.Errorf("expected ID 'debt_123', got %s", debt.ID)
	}
}

func TestDebtService_GetPayableDebtByID(t *testing.T) {
	debtRepository := &mockDebtRepository{
		findByIDFn: func(ctx context.Context, id string) (*domain.PayableDebt, error) {
			return &domain.PayableDebt{
				ID:          id,
				TenantID:    "tnt_1",
				OrderID:     "ord_1",
				TotalAmount: 100.0,
				PaidAmount:  0.0,
				Currency:    "USD",
				Status:      domain.DebtStatusUnpaid,
			}, nil
		},
	}
	debtService := NewDebtService(nil, debtRepository, nil)

	debt, err := debtService.GetPayableDebtByID(context.Background(), "debt_123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if debt.ID != "debt_123" {
		t.Errorf("expected ID 'debt_123', got %s", debt.ID)
	}
}
