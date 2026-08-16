package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"payment-service/internal/infrastructure/postgres"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type OutboxMessage struct {
	EventID     string
	RoutingKey  string
	Payload     []byte
	Status      string
	RetryCount  int
	LastError   string
	CreatedAt   time.Time
	PublishedAt *time.Time
}

type OutboxRepository struct {
	dbClient *postgres.Client
}

func NewOutboxRepository(dbClient *postgres.Client) *OutboxRepository {
	return &OutboxRepository{dbClient: dbClient}
}

func (outboxRepository *OutboxRepository) SaveOutboxEvent(ctx context.Context, eventID, routingKey string, payload any) error {
	exec := txcontext.GetExecutor(ctx, outboxRepository.dbClient)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal outbox payload: %w", err)
	}

	query := `
		INSERT INTO payment_outbox (event_id, routing_key, payload, status, created_at)
		VALUES ($1, $2, $3, 'PENDING', $4)
	`

	_, err = exec.ExecContext(ctx, query, eventID, routingKey, payloadBytes, time.Now())
	if err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}

	return nil
}

func (outboxRepository *OutboxRepository) FetchPending(ctx context.Context, limit int) ([]*OutboxMessage, error) {
	exec := txcontext.GetExecutor(ctx, outboxRepository.dbClient)

	query := `
		SELECT event_id, routing_key, payload, status, retry_count, last_error, created_at, published_at
		FROM payment_outbox
		WHERE status = 'PENDING'
		ORDER BY created_at ASC
		LIMIT $1
	`

	rows, err := exec.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pending outbox events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var messages []*OutboxMessage
	for rows.Next() {
		var msg OutboxMessage
		err := rows.Scan(
			&msg.EventID, &msg.RoutingKey, &msg.Payload, &msg.Status,
			&msg.RetryCount, &msg.LastError, &msg.CreatedAt, &msg.PublishedAt,
		)
		if err != nil {
			return nil, err
		}
		messages = append(messages, &msg)
	}

	return messages, nil
}

func (outboxRepository *OutboxRepository) MarkPublished(ctx context.Context, eventID string) error {
	exec := txcontext.GetExecutor(ctx, outboxRepository.dbClient)
	now := time.Now()

	query := `
		UPDATE payment_outbox SET
			status = 'PUBLISHED',
			published_at = $1,
			last_error = ''
		WHERE event_id = $2
	`

	_, err := exec.ExecContext(ctx, query, now, eventID)
	return err
}

func (outboxRepository *OutboxRepository) MarkFailed(ctx context.Context, eventID string, reason string) error {
	exec := txcontext.GetExecutor(ctx, outboxRepository.dbClient)

	query := `
		UPDATE payment_outbox SET
			retry_count = retry_count + 1,
			last_error = $1
		WHERE event_id = $2
	`

	_, err := exec.ExecContext(ctx, query, reason, eventID)
	return err
}
