package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/txcontext"

	"github.com/lib/pq"
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

// OutboxRepository manages the per-tenant outbox table in order-service.
// Unlike user-service (which uses a fixed public.outbox), this repository
// targets {{SCHEMA_NAME}}.outbox using the dynamic schema injected via tenantdb.Config.
type OutboxRepository struct {
	config tenantdb.Config
}

func NewOutboxRepository(config tenantdb.Config) *OutboxRepository {
	return &OutboxRepository{config: config}
}

func (r *OutboxRepository) schemaName() string {
	if r.config.SchemaName == "" {
		return "public"
	}
	return r.config.SchemaName
}

func (r *OutboxRepository) CreateOutboxMessage(ctx context.Context, input CreateOutboxMessageInput) error {
	exec := txcontext.GetExecutor(ctx, r.config.DB)
	query := fmt.Sprintf(`
		INSERT INTO %s.outbox (
			id, tenant_id, aggregate_type, aggregate_id, event_type, payload, status, retry_count
		) VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', 0);
	`, pq.QuoteIdentifier(r.schemaName()))

	if _, err := exec.ExecContext(ctx, query,
		input.ID, input.TenantID, input.AggregateType, input.AggregateID, input.EventType, string(input.Payload),
	); err != nil {
		return fmt.Errorf("outbox repository: failed to insert outbox message: %w", err)
	}
	return nil
}

func (r *OutboxRepository) FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
	exec := txcontext.GetExecutor(ctx, r.config.DB)
	schema := pq.QuoteIdentifier(r.schemaName())
	query := fmt.Sprintf(`
		WITH claimed AS (
			UPDATE %s.outbox
			SET status     = 'PROCESSING',
			    claimed_at = NOW()
			WHERE id IN (
				SELECT id
				FROM   %s.outbox
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
	`, schema, schema)

	rows, err := exec.QueryContext(ctx, query, eventType, maxRetries, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox repository: failed to fetch and claim batch: %w", err)
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
			return nil, fmt.Errorf("outbox repository: failed to scan outbox row: %w", err)
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
	exec := txcontext.GetExecutor(ctx, r.config.DB)
	query := fmt.Sprintf(`
		UPDATE %s.outbox
		SET status     = 'PENDING',
		    claimed_at = NULL
		WHERE status     = 'PROCESSING'
		  AND event_type = $1
		  AND claimed_at < NOW() - $2::interval;
	`, pq.QuoteIdentifier(r.schemaName()))

	_, err := exec.ExecContext(ctx, query, eventType, fmt.Sprintf("%d seconds", int(stuckClaimTimeout.Seconds())))
	if err != nil {
		return fmt.Errorf("outbox repository: failed to recover stuck claimed rows: %w", err)
	}
	return nil
}

func (r *OutboxRepository) MarkPublished(ctx context.Context, id string) error {
	exec := txcontext.GetExecutor(ctx, r.config.DB)
	query := fmt.Sprintf(`
		UPDATE %s.outbox
		SET status       = 'PUBLISHED',
		    claimed_at   = NULL,
		    processed_at = NOW()
		WHERE id = $1;
	`, pq.QuoteIdentifier(r.schemaName()))

	if _, err := exec.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("outbox repository: failed to mark as published: %w", err)
	}
	return nil
}

func (r *OutboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	exec := txcontext.GetExecutor(ctx, r.config.DB)
	safeErr := sanitizeError(err)
	query := fmt.Sprintf(`
		UPDATE %s.outbox
		SET retry_count   = retry_count + 1,
		    last_error    = $2,
		    claimed_at    = NULL,
		    next_retry_at = CASE
		        WHEN retry_count + 1 < $3
		        THEN NOW() + (INTERVAL '1 second' * POWER(2, retry_count + 1))
		        END,
		    status = CASE
		        WHEN retry_count + 1 >= $3 THEN 'FAILED'
		        ELSE 'PENDING'
		    END
		WHERE id = $1;
	`, pq.QuoteIdentifier(r.schemaName()))

	if _, dbErr := exec.ExecContext(ctx, query, id, safeErr, maxRetries); dbErr != nil {
		return fmt.Errorf("outbox repository: failed to mark as failed: %w", dbErr)
	}
	return nil
}
