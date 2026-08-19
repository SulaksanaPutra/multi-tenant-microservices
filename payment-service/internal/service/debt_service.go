package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
)

type DebtRepository interface {
	Create(ctx context.Context, input repository.CreateDebtInput) error
	Update(ctx context.Context, input repository.UpdateDebtInput) error
	FindByID(ctx context.Context, id string) (*domain.PayableDebt, error)
	FindByIDForUpdate(ctx context.Context, id string) (*domain.PayableDebt, error)
	FindByOrderID(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error)
	FindByOrderIDForUpdate(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error)
}

type DebtService struct {
	txManager      TxManager
	debtRepository DebtRepository
	logger         *slog.Logger
}

func NewDebtService(
	txManager TxManager,
	debtRepository DebtRepository,
	logger *slog.Logger,
) *DebtService {
	if logger == nil {
		logger = slog.Default()
	}
	return &DebtService{
		txManager:      txManager,
		debtRepository: debtRepository,
		logger:         logger,
	}
}

type CreatePayableDebtInput struct {
	TenantID string
	OrderID  string
	Amount   float64
	Currency string
}

type UpdatePayableDebtInput struct {
	ID         string
	PaidAmount float64
	Status     domain.DebtStatus
}

type PayableDebtOutput struct {
	ID          string
	TenantID    string
	OrderID     string
	TotalAmount float64
	PaidAmount  float64
	Currency    string
	Status      domain.DebtStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func toDebtOutput(d *domain.PayableDebt) *PayableDebtOutput {
	if d == nil {
		return nil
	}
	return &PayableDebtOutput{
		ID:          d.ID,
		TenantID:    d.TenantID,
		OrderID:     d.OrderID,
		TotalAmount: d.TotalAmount,
		PaidAmount:  d.PaidAmount,
		Currency:    d.Currency,
		Status:      d.Status,
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	}
}

func (debtService *DebtService) CreatePayableDebt(ctx context.Context, input CreatePayableDebtInput) (*PayableDebtOutput, error) {
	if input.TenantID == "" || input.OrderID == "" {
		return nil, errors.New("tenant_id and order_id are required")
	}

	currency := input.Currency
	if currency == "" {
		currency = "USD"
	}

	var debt *domain.PayableDebt
	actionFn := func(txCtx context.Context) error {
		existing, err := debtService.debtRepository.FindByOrderID(txCtx, input.TenantID, input.OrderID)
		if err == nil && existing != nil {
			debt = existing
			return nil
		}

		now := time.Now()
		debt = &domain.PayableDebt{
			ID:          domain.GenerateDebtID(),
			TenantID:    input.TenantID,
			OrderID:     input.OrderID,
			TotalAmount: input.Amount,
			PaidAmount:  0.0,
			Currency:    currency,
			Status:      domain.DebtStatusUnpaid,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		return debtService.debtRepository.Create(txCtx, repository.CreateDebtInput{
			ID:          debt.ID,
			TenantID:    debt.TenantID,
			OrderID:     debt.OrderID,
			TotalAmount: debt.TotalAmount,
			PaidAmount:  debt.PaidAmount,
			Currency:    debt.Currency,
			Status:      debt.Status,
			CreatedAt:   debt.CreatedAt,
			UpdatedAt:   debt.UpdatedAt,
		})
	}

	var err error
	if debtService.txManager != nil {
		err = debtService.txManager.WithTransaction(ctx, actionFn)
	} else {
		err = actionFn(ctx)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create payable debt: %w", err)
	}

	return toDebtOutput(debt), nil
}

func (debtService *DebtService) GetPayableDebtByOrderID(ctx context.Context, tenantID, orderID string) (*PayableDebtOutput, error) {
	d, err := debtService.debtRepository.FindByOrderID(ctx, tenantID, orderID)
	return toDebtOutput(d), err
}

func (debtService *DebtService) GetPayableDebtByID(ctx context.Context, id string) (*PayableDebtOutput, error) {
	d, err := debtService.debtRepository.FindByID(ctx, id)
	return toDebtOutput(d), err
}
