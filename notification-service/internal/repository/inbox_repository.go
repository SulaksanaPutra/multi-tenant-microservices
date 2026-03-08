package repository

import (
	"context"
	"fmt"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/txcontext"
)

type CreateInboxMessageInput struct {
	EventID   string
	TenantID  string
	EventType string
	Payload   []byte
}

type InboxRepository struct {
	dbClient *postgres.Client
}

func NewInboxRepository(dbClient *postgres.Client) *InboxRepository {
	return &InboxRepository{dbClient: dbClient}
}

func (r *InboxRepository) AcquireTenantLock(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return nil
	}
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `SELECT pg_advisory_xact_lock(hashtext($1));`
	if _, err := exec.ExecContext(ctx, query, tenantID); err != nil {
		return fmt.Errorf("failed to acquire pg_advisory_xact_lock for tenant_id='%s': %w", tenantID, err)
	}
	return nil
}

func (r *InboxRepository) TryInsert(ctx context.Context, input CreateInboxMessageInput) (bool, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		INSERT INTO public.inbox (event_id, tenant_id, event_type, payload)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id) DO NOTHING;
	`
	payloadStr := string(input.Payload)
	if payloadStr == "" {
		payloadStr = "{}"
	}
	res, err := exec.ExecContext(ctx, query, input.EventID, input.TenantID, input.EventType, payloadStr)
	if err != nil {
		return false, fmt.Errorf("failed to insert inbox record: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to check rows affected in inbox insert: %w", err)
	}
	if rows == 0 {
		return true, nil // isDuplicate = true
	}
	return false, nil // isDuplicate = false, safe to process
}

func (r *InboxRepository) GetEventsByTenantID(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		SELECT event_id, tenant_id, event_type, payload
		FROM public.inbox
		WHERE tenant_id = $1;
	`
	rows, err := exec.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query inbox events for tenant_id='%s': %w", tenantID, err)
	}
	if rows == nil {
		return []domain.InboxMessage{}, nil
	}
	defer rows.Close()

	var list []domain.InboxMessage
	for rows.Next() {
		var msg domain.InboxMessage
		var rawPayload string
		if err := rows.Scan(&msg.EventID, &msg.TenantID, &msg.EventType, &rawPayload); err != nil {
			return nil, fmt.Errorf("failed to scan inbox row: %w", err)
		}
		msg.Payload = []byte(rawPayload)
		list = append(list, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error reading inbox events: %w", err)
	}

	if list == nil {
		list = []domain.InboxMessage{}
	}

	return list, nil
}
