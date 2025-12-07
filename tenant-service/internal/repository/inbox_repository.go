package repository

import (
	"context"
	"fmt"

	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/txcontext"
)

type InboxRepository struct {
	client *postgres.Client
}

func NewInboxRepository(client *postgres.Client) *InboxRepository {
	return &InboxRepository{client: client}
}

func (r *InboxRepository) TryInsert(ctx context.Context, eventID string) (bool, error) {
	exec := txcontext.GetExecutor(ctx, r.client)
	const query = `INSERT INTO public.inbox (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING;`
	res, err := exec.ExecContext(ctx, query, eventID)
	if err != nil {
		return false, fmt.Errorf("failed to insert event_id into inbox: %w", err)
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
