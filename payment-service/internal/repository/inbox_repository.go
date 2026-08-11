package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"payment-service/internal/domain"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type InboxRepository struct {
	db *sql.DB
}

func NewInboxRepository(db *sql.DB) *InboxRepository {
	return &InboxRepository{db: db}
}

func (r *InboxRepository) SaveInboxEvent(ctx context.Context, eventID, eventType string) error {
	exec := txcontext.GetExecutor(ctx, r.db)

	query := `
		INSERT INTO payment_inbox (event_id, event_type, processed_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id) DO NOTHING
	`

	res, err := exec.ExecContext(ctx, query, eventID, eventType, time.Now())
	if err != nil {
		return fmt.Errorf("failed to insert inbox event: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrDuplicateEvent
	}

	return nil
}
