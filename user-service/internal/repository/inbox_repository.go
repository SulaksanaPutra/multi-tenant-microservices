package repository

import (
	"context"
	"fmt"

	"github.com/lib/pq"
	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txctx"
)

type InboxRepository interface {
	TryInsert(ctx context.Context, eventID string) (isDuplicate bool, err error)
}

type inboxRepository struct {
	client *postgres.Client
}

func NewInboxRepository(client *postgres.Client) InboxRepository {
	return &inboxRepository{client: client}
}

func (r *inboxRepository) TryInsert(ctx context.Context, eventID string) (bool, error) {
	exec := txctx.GetExecutor(ctx, r.client.DB)
	const query = `INSERT INTO public.inbox (event_id) VALUES ($1);`
	_, err := exec.ExecContext(ctx, query, eventID)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return true, nil // isDuplicate = true
		}
		return false, fmt.Errorf("failed to insert event_id into inbox: %w", err)
	}
	return false, nil // isDuplicate = false, safe to process
}
