package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/postgres"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type DebtRepository struct {
	dbClient *postgres.Client
}

func NewDebtRepository(dbClient *postgres.Client) *DebtRepository {
	return &DebtRepository{dbClient: dbClient}
}

type CreateDebtInput struct {
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

type UpdateDebtInput struct {
	ID         string
	PaidAmount float64
	Status     domain.DebtStatus
	UpdatedAt  time.Time
}

func (debtRepository *DebtRepository) Create(ctx context.Context, input CreateDebtInput) error {
	exec := txcontext.GetExecutor(ctx, debtRepository.dbClient)

	now := time.Now()
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := now

	query := `
		INSERT INTO payable_debts (
			id, tenant_id, order_id, total_amount, paid_amount, currency, status,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := exec.ExecContext(ctx, query,
		input.ID, input.TenantID, input.OrderID, input.TotalAmount, input.PaidAmount,
		input.Currency, string(input.Status), createdAt, updatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert payable debt: %w", err)
	}

	return nil
}

func (debtRepository *DebtRepository) Update(ctx context.Context, input UpdateDebtInput) error {
	exec := txcontext.GetExecutor(ctx, debtRepository.dbClient)

	updatedAt := input.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}

	query := `
		UPDATE payable_debts SET
			paid_amount = $1,
			status = $2,
			updated_at = $3
		WHERE id = $4
	`

	res, err := exec.ExecContext(ctx, query,
		input.PaidAmount, string(input.Status), updatedAt, input.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update payable debt: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrDebtNotFound
	}

	return nil
}

func (debtRepository *DebtRepository) FindByID(ctx context.Context, id string) (*domain.PayableDebt, error) {
	exec := txcontext.GetExecutor(ctx, debtRepository.dbClient)
	query := `
		SELECT id, tenant_id, order_id, total_amount, paid_amount, currency, status,
		       created_at, updated_at
		FROM payable_debts WHERE id = $1
	`
	return debtRepository.scanDebt(exec.QueryRowContext(ctx, query, id))
}

func (debtRepository *DebtRepository) FindByIDForUpdate(ctx context.Context, id string) (*domain.PayableDebt, error) {
	exec := txcontext.GetExecutor(ctx, debtRepository.dbClient)
	query := `
		SELECT id, tenant_id, order_id, total_amount, paid_amount, currency, status,
		       created_at, updated_at
		FROM payable_debts WHERE id = $1 FOR UPDATE
	`
	return debtRepository.scanDebt(exec.QueryRowContext(ctx, query, id))
}

func (debtRepository *DebtRepository) FindByOrderID(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error) {
	exec := txcontext.GetExecutor(ctx, debtRepository.dbClient)
	query := `
		SELECT id, tenant_id, order_id, total_amount, paid_amount, currency, status,
		       created_at, updated_at
		FROM payable_debts WHERE tenant_id = $1 AND order_id = $2
	`
	return debtRepository.scanDebt(exec.QueryRowContext(ctx, query, tenantID, orderID))
}

func (debtRepository *DebtRepository) FindByOrderIDForUpdate(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error) {
	exec := txcontext.GetExecutor(ctx, debtRepository.dbClient)
	query := `
		SELECT id, tenant_id, order_id, total_amount, paid_amount, currency, status,
		       created_at, updated_at
		FROM payable_debts WHERE tenant_id = $1 AND order_id = $2 FOR UPDATE
	`
	return debtRepository.scanDebt(exec.QueryRowContext(ctx, query, tenantID, orderID))
}

func (debtRepository *DebtRepository) scanDebt(row *sql.Row) (*domain.PayableDebt, error) {
	var d domain.PayableDebt
	var statusStr string

	err := row.Scan(
		&d.ID, &d.TenantID, &d.OrderID, &d.TotalAmount, &d.PaidAmount,
		&d.Currency, &statusStr, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrDebtNotFound
		}
		return nil, fmt.Errorf("failed to scan payable debt: %w", err)
	}

	d.Status = domain.DebtStatus(statusStr)
	return &d, nil
}
