package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/txcontext"
)

type PaymentRepository struct {
	db *sql.DB
}

func NewPaymentRepository(db *sql.DB) *PaymentRepository {
	return &PaymentRepository{db: db}
}

func (r *PaymentRepository) Create(ctx context.Context, p *domain.Payment) error {
	exec := txcontext.GetExecutor(ctx, r.db)

	instructionsJSON, err := json.Marshal(p.Instructions)
	if err != nil {
		return fmt.Errorf("failed to marshal instructions: %w", err)
	}

	payloadJSON, err := json.Marshal(p.RawWebhookPayload)
	if err != nil {
		payloadJSON = []byte("{}")
	}

	query := `
		INSERT INTO payments (
			id, tenant_id, order_id, amount, currency, status,
			provider, external_id, payment_instructions, raw_webhook_payload,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`

	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now

	_, err = exec.ExecContext(ctx, query,
		p.ID, p.TenantID, p.OrderID, p.Amount, p.Currency, string(p.Status),
		string(p.Provider), p.ExternalID, instructionsJSON, payloadJSON,
		p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert payment: %w", err)
	}

	return nil
}

func (r *PaymentRepository) Update(ctx context.Context, p *domain.Payment) error {
	exec := txcontext.GetExecutor(ctx, r.db)

	instructionsJSON, err := json.Marshal(p.Instructions)
	if err != nil {
		return fmt.Errorf("failed to marshal instructions: %w", err)
	}

	payloadJSON, err := json.Marshal(p.RawWebhookPayload)
	if err != nil {
		payloadJSON = []byte("{}")
	}

	p.UpdatedAt = time.Now()

	query := `
		UPDATE payments SET
			status = $1,
			provider = $2,
			external_id = $3,
			payment_instructions = $4,
			raw_webhook_payload = $5,
			updated_at = $6
		WHERE id = $7
	`

	res, err := exec.ExecContext(ctx, query,
		string(p.Status), string(p.Provider), p.ExternalID,
		instructionsJSON, payloadJSON, p.UpdatedAt, p.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update payment: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrPaymentNotFound
	}

	return nil
}

func (r *PaymentRepository) FindByID(ctx context.Context, id string) (*domain.Payment, error) {
	exec := txcontext.GetExecutor(ctx, r.db)
	query := `
		SELECT id, tenant_id, order_id, amount, currency, status,
		       provider, external_id, payment_instructions, raw_webhook_payload,
		       created_at, updated_at
		FROM payments WHERE id = $1
	`
	return r.scanPayment(exec.QueryRowContext(ctx, query, id))
}

// FindByIDForUpdate locks the payment row for pessimistic concurrency control during webhook processing.
func (r *PaymentRepository) FindByIDForUpdate(ctx context.Context, id string) (*domain.Payment, error) {
	exec := txcontext.GetExecutor(ctx, r.db)
	query := `
		SELECT id, tenant_id, order_id, amount, currency, status,
		       provider, external_id, payment_instructions, raw_webhook_payload,
		       created_at, updated_at
		FROM payments WHERE id = $1 FOR UPDATE
	`
	return r.scanPayment(exec.QueryRowContext(ctx, query, id))
}

func (r *PaymentRepository) FindByOrderID(ctx context.Context, tenantID, orderID string) (*domain.Payment, error) {
	exec := txcontext.GetExecutor(ctx, r.db)
	query := `
		SELECT id, tenant_id, order_id, amount, currency, status,
		       provider, external_id, payment_instructions, raw_webhook_payload,
		       created_at, updated_at
		FROM payments WHERE tenant_id = $1 AND order_id = $2
		ORDER BY created_at DESC LIMIT 1
	`
	return r.scanPayment(exec.QueryRowContext(ctx, query, tenantID, orderID))
}

func (r *PaymentRepository) FindExpiredPayments(ctx context.Context, ttlDuration time.Duration, limit int) ([]*domain.Payment, error) {
	exec := txcontext.GetExecutor(ctx, r.db)
	cutoff := time.Now().Add(-ttlDuration)

	query := `
		SELECT id, tenant_id, order_id, amount, currency, status,
		       provider, external_id, payment_instructions, raw_webhook_payload,
		       created_at, updated_at
		FROM payments
		WHERE status IN ('PENDING', 'PAYMENT_INSTRUCTIONS_READY') AND created_at <= $1
		ORDER BY created_at ASC LIMIT $2
	`

	rows, err := exec.QueryContext(ctx, query, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query expired payments: %w", err)
	}
	defer rows.Close()

	var payments []*domain.Payment
	for rows.Next() {
		p, err := r.scanPaymentRow(rows)
		if err != nil {
			return nil, err
		}
		payments = append(payments, p)
	}

	return payments, nil
}

func (r *PaymentRepository) CreateAttempt(ctx context.Context, att *domain.PaymentAttempt) error {
	exec := txcontext.GetExecutor(ctx, r.db)
	query := `
		INSERT INTO payment_attempts (
			id, payment_id, tenant_id, provider, external_session_id,
			status, error_message, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	now := time.Now()
	if att.CreatedAt.IsZero() {
		att.CreatedAt = now
	}
	att.UpdatedAt = now

	_, err := exec.ExecContext(ctx, query,
		att.ID, att.PaymentID, att.TenantID, string(att.Provider),
		att.ExternalSessionID, string(att.Status), att.ErrorMessage,
		att.CreatedAt, att.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert payment attempt: %w", err)
	}

	return nil
}

func (r *PaymentRepository) UpdateAttempt(ctx context.Context, att *domain.PaymentAttempt) error {
	exec := txcontext.GetExecutor(ctx, r.db)
	att.UpdatedAt = time.Now()

	query := `
		UPDATE payment_attempts SET
			external_session_id = $1,
			status = $2,
			error_message = $3,
			updated_at = $4
		WHERE id = $5
	`

	_, err := exec.ExecContext(ctx, query,
		att.ExternalSessionID, string(att.Status), att.ErrorMessage,
		att.UpdatedAt, att.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update payment attempt: %w", err)
	}

	return nil
}

func (r *PaymentRepository) FindAttemptsByPaymentID(ctx context.Context, paymentID string) ([]*domain.PaymentAttempt, error) {
	exec := txcontext.GetExecutor(ctx, r.db)
	query := `
		SELECT id, payment_id, tenant_id, provider, external_session_id,
		       status, error_message, created_at, updated_at
		FROM payment_attempts WHERE payment_id = $1
		ORDER BY created_at ASC
	`

	rows, err := exec.QueryContext(ctx, query, paymentID)
	if err != nil {
		return nil, fmt.Errorf("failed to query attempts: %w", err)
	}
	defer rows.Close()

	var attempts []*domain.PaymentAttempt
	for rows.Next() {
		var att domain.PaymentAttempt
		var provStr, statusStr string
		err := rows.Scan(
			&att.ID, &att.PaymentID, &att.TenantID, &provStr,
			&att.ExternalSessionID, &statusStr, &att.ErrorMessage,
			&att.CreatedAt, &att.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		att.Provider = domain.ProviderType(provStr)
		att.Status = domain.AttemptStatus(statusStr)
		attempts = append(attempts, &att)
	}

	return attempts, nil
}

func (r *PaymentRepository) scanPayment(row *sql.Row) (*domain.Payment, error) {
	var p domain.Payment
	var statusStr, provStr string
	var instructionsBytes, payloadBytes []byte

	err := row.Scan(
		&p.ID, &p.TenantID, &p.OrderID, &p.Amount, &p.Currency, &statusStr,
		&provStr, &p.ExternalID, &instructionsBytes, &payloadBytes,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrPaymentNotFound
		}
		return nil, fmt.Errorf("failed to scan payment: %w", err)
	}

	p.Status = domain.PaymentStatus(statusStr)
	p.Provider = domain.ProviderType(provStr)

	if len(instructionsBytes) > 0 {
		_ = json.Unmarshal(instructionsBytes, &p.Instructions)
	}
	if len(payloadBytes) > 0 {
		_ = json.Unmarshal(payloadBytes, &p.RawWebhookPayload)
	}

	return &p, nil
}

func (r *PaymentRepository) scanPaymentRow(rows *sql.Rows) (*domain.Payment, error) {
	var p domain.Payment
	var statusStr, provStr string
	var instructionsBytes, payloadBytes []byte

	err := rows.Scan(
		&p.ID, &p.TenantID, &p.OrderID, &p.Amount, &p.Currency, &statusStr,
		&provStr, &p.ExternalID, &instructionsBytes, &payloadBytes,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan payment row: %w", err)
	}

	p.Status = domain.PaymentStatus(statusStr)
	p.Provider = domain.ProviderType(provStr)

	if len(instructionsBytes) > 0 {
		_ = json.Unmarshal(instructionsBytes, &p.Instructions)
	}
	if len(payloadBytes) > 0 {
		_ = json.Unmarshal(payloadBytes, &p.RawWebhookPayload)
	}

	return &p, nil
}
