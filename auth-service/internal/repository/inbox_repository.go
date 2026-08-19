package repository

import (
	"context"
	"fmt"

	"auth-service/internal/infrastructure/postgres"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
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

func (inboxRepository *InboxRepository) TryInsert(ctx context.Context, input CreateInboxMessageInput) (bool, error) {
	exec := txcontext.GetExecutor(ctx, inboxRepository.dbClient)
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
		return true, nil
	}
	return false, nil
}
