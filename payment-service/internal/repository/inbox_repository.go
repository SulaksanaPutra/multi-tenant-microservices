package repository

import (
	"context"
	"fmt"

	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/postgres"
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

// TryInsert inserts the event into payment_inbox atomically. Returns isDuplicate
// = true when the event_id already exists (dedup barrier satisfied).
func (r *InboxRepository) TryInsert(ctx context.Context, input CreateInboxMessageInput) (bool, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		INSERT INTO payment_inbox (event_id, tenant_id, event_type, payload)
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

// SaveInboxEvent preserves backward compatibility for legacy callers by wrapping TryInsert.
func (r *InboxRepository) SaveInboxEvent(ctx context.Context, eventID, eventType string) error {
	isDuplicate, err := r.TryInsert(ctx, CreateInboxMessageInput{
		EventID:   eventID,
		EventType: eventType,
	})
	if err != nil {
		return err
	}
	if isDuplicate {
		return domain.ErrDuplicateEvent
	}
	return nil
}
