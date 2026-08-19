package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
	"payment-service/internal/domain"
	"payment-service/internal/infrastructure/postgres"
)

const (
	maxRetries        = 5
	maxErrorLength    = 500
	stuckClaimTimeout = 30 * time.Second
)

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|pwd)\s*=\s*\S+`),
	regexp.MustCompile(`(?i)(host|port|user|sslmode)\s*=\s*\S+`),
	regexp.MustCompile(`amqp://\S+`),
	regexp.MustCompile(`postgres://\S+`),
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, p := range sensitivePatterns {
		msg = p.ReplaceAllString(msg, "[redacted]")
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > maxErrorLength {
		msg = msg[:maxErrorLength] + " [truncated]"
	}
	return msg
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
		INSERT INTO payment_outbox (event_id, routing_key, payload, status, retry_count, created_at)
		VALUES ($1, $2, $3, 'PENDING', 0, NOW())
	`

	_, err = exec.ExecContext(ctx, query, eventID, routingKey, payloadBytes)
	if err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}

	return nil
}

func (outboxRepository *OutboxRepository) ListPending(ctx context.Context, limit int) ([]*domain.OutboxMessage, error) {
	exec := txcontext.GetExecutor(ctx, outboxRepository.dbClient)

	query := `
		WITH claimed AS (
			UPDATE payment_outbox
			SET status     = 'PROCESSING',
			    claimed_at = NOW()
			WHERE event_id IN (
				SELECT event_id
				FROM payment_outbox
				WHERE status      = 'PENDING'
				  AND retry_count < $1
				  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
				ORDER BY created_at ASC
				LIMIT $2
				FOR UPDATE SKIP LOCKED
			)
			RETURNING event_id, routing_key, payload, status, retry_count, last_error, created_at, published_at
		)
		SELECT event_id, routing_key, payload, status, retry_count, last_error, created_at, published_at
		FROM claimed;
	`

	rows, err := exec.QueryContext(ctx, query, maxRetries, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pending outbox events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var messages []*domain.OutboxMessage
	for rows.Next() {
		var msg domain.OutboxMessage
		err := rows.Scan(
			&msg.EventID, &msg.RoutingKey, &msg.Payload, &msg.Status,
			&msg.RetryCount, &msg.LastError, &msg.CreatedAt, &msg.PublishedAt,
		)
		if err != nil {
			return nil, err
		}
		messages = append(messages, &msg)
	}

	return messages, rows.Err()
}

func (outboxRepository *OutboxRepository) RecoverStuckClaims(ctx context.Context) error {
	exec := txcontext.GetExecutor(ctx, outboxRepository.dbClient)
	const query = `
		UPDATE payment_outbox
		SET status     = 'PENDING',
		    claimed_at = NULL
		WHERE status     = 'PROCESSING'
		  AND claimed_at < NOW() - $1::interval;
	`
	_, err := exec.ExecContext(ctx, query, fmt.Sprintf("%d seconds", int(stuckClaimTimeout.Seconds())))
	if err != nil {
		return fmt.Errorf("failed to recover stuck claimed outbox rows: %w", err)
	}
	return nil
}

func (outboxRepository *OutboxRepository) MarkPublished(ctx context.Context, eventID string) error {
	exec := txcontext.GetExecutor(ctx, outboxRepository.dbClient)

	query := `
		UPDATE payment_outbox SET
			status       = 'PUBLISHED',
			claimed_at   = NULL,
			published_at = NOW(),
			last_error   = ''
		WHERE event_id = $1
	`

	_, err := exec.ExecContext(ctx, query, eventID)
	return err
}

func (outboxRepository *OutboxRepository) MarkFailed(ctx context.Context, eventID string, reason string) error {
	exec := txcontext.GetExecutor(ctx, outboxRepository.dbClient)

	query := `
		UPDATE payment_outbox SET
			retry_count   = retry_count + 1,
			last_error    = $1,
			claimed_at    = NULL,
			next_retry_at = CASE
				WHEN retry_count + 1 < $2
				THEN NOW() + (INTERVAL '1 second' * POWER(2, retry_count + 1))
			END,
			status = CASE
				WHEN retry_count + 1 >= $2 THEN 'FAILED'
				ELSE 'PENDING'
			END
		WHERE event_id = $3
	`

	_, err := exec.ExecContext(ctx, query, reason, maxRetries, eventID)
	return err
}
