package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txcontext"
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

type CreateOutboxMessageInput struct {
	ID            string
	TenantID      string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
}

type OutboxRepository struct {
	dbClient *postgres.Client
}

func NewOutboxRepository(dbClient *postgres.Client) *OutboxRepository {
	return &OutboxRepository{dbClient: dbClient}
}

func (r *OutboxRepository) CreateOutboxMessage(ctx context.Context, input CreateOutboxMessageInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		INSERT INTO public.outbox (
			id, tenant_id, aggregate_type, aggregate_id, event_type, payload, status, retry_count
		) VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', 0);
	`
	if _, err := exec.ExecContext(ctx, query,
		input.ID, input.TenantID, input.AggregateType, input.AggregateID, input.EventType, string(input.Payload),
	); err != nil {
		return fmt.Errorf("failed to insert outbox message in transaction: %w", err)
	}
	return nil
}

func (r *OutboxRepository) FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		WITH claimed AS (
			UPDATE public.outbox
			SET status     = 'PROCESSING',
			    claimed_at = NOW()
			WHERE id IN (
				SELECT id
				FROM   public.outbox
				WHERE  status      = 'PENDING'
				  AND  event_type  = $1
				  AND  retry_count < $2
				  AND  (next_retry_at IS NULL OR next_retry_at <= NOW())
				ORDER BY created_at ASC
				LIMIT $3
				FOR UPDATE SKIP LOCKED
			)
			RETURNING id, tenant_id, aggregate_type, aggregate_id, event_type,
			          payload, status, retry_count, created_at
		)
		SELECT * FROM claimed;
	`
	rows, err := exec.QueryContext(ctx, query, eventType, maxRetries, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch and claim outbox batch: %w", err)
	}
	if rows == nil {
		return []domain.OutboxMessage{}, nil
	}
	defer func(r *sql.Rows) {
		if r != nil {
			_ = r.Close()
		}
	}(rows)

	var list []domain.OutboxMessage
	for rows.Next() {
		var msg domain.OutboxMessage
		var payloadStr string
		if err := rows.Scan(
			&msg.ID, &msg.TenantID, &msg.AggregateType, &msg.AggregateID, &msg.EventType,
			&payloadStr, &msg.Status, &msg.RetryCount, &msg.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan outbox row: %w", err)
		}
		msg.Payload = []byte(payloadStr)
		list = append(list, msg)
	}
	if list == nil {
		list = []domain.OutboxMessage{}
	}
	return list, rows.Err()
}

func (r *OutboxRepository) RecoverStuckClaims(ctx context.Context, eventType string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		UPDATE public.outbox
		SET status     = 'PENDING',
		    claimed_at = NULL
		WHERE status     = 'PROCESSING'
		  AND event_type = $1
		  AND claimed_at < NOW() - $2::interval;
	`
	_, err := exec.ExecContext(ctx, query, eventType, fmt.Sprintf("%d seconds", int(stuckClaimTimeout.Seconds())))
	if err != nil {
		return fmt.Errorf("failed to recover stuck claimed outbox rows: %w", err)
	}
	return nil
}

func (r *OutboxRepository) MarkPublished(ctx context.Context, id string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		UPDATE public.outbox
		SET status       = 'PUBLISHED',
		    claimed_at   = NULL,
		    processed_at = NOW()
		WHERE id = $1;
	`
	if _, err := exec.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("failed to mark outbox record as published: %w", err)
	}
	return nil
}

func (r *OutboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	safeErr := sanitizeError(err)
	const query = `
			UPDATE public.outbox
			SET retry_count  = retry_count + 1,
				last_error   = $2,
				claimed_at   = NULL,
				next_retry_at = CASE
					WHEN retry_count + 1 < $3
					THEN NOW() + (INTERVAL '1 second' * POWER(2, retry_count + 1))
					END,
				status = CASE
					WHEN retry_count + 1 >= $3 THEN 'FAILED'
					ELSE 'PENDING'
				END
			WHERE id = $1;
		`
	if _, dbErr := exec.ExecContext(ctx, query, id, safeErr, maxRetries); dbErr != nil {
		return fmt.Errorf("failed to mark outbox record as failed: %w", dbErr)
	}
	return nil
}
