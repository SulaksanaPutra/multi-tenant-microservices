package repository

import (
	"context"
	"fmt"

	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txcontext"
)

type InboxRepository struct {
	dbClient *postgres.Client
}

func NewInboxRepository(dbClient *postgres.Client) *InboxRepository {
	return &InboxRepository{dbClient: dbClient}
}

func (r *InboxRepository) TryInsert(ctx context.Context, eventID string) (bool, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
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
