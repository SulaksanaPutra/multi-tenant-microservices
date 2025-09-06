package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	maxRetries        = 5
	maxErrorLength    = 500
	stuckClaimTimeout = 30 * time.Second
)

// sensitivePatterns scrubs known sensitive data from error strings before
// persisting to the database (Fix #5: prevents infra topology leakage).
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

type OutboxMessage struct {
	ID            string
	TenantID      *string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	Status        string
	RetryCount    int
	LastError     *string
	NextRetryAt   *time.Time
	ClaimedAt     *time.Time
	CreatedAt     time.Time
	ProcessedAt   *time.Time
}

type OutboxRepository interface {
	// CreateOutboxMessage writes an outbox record inside an existing database transaction.
	CreateOutboxMessage(ctx context.Context, tx *sql.Tx, msg OutboxMessage) error
	// FetchAndClaimBatch atomically claims a batch of PENDING messages from the default DB.
	FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]OutboxMessage, error)
	// FetchAndClaimBatchFromDB atomically claims a batch from a specific target DB pool.
	FetchAndClaimBatchFromDB(ctx context.Context, targetDB *sql.DB, eventType string, limit int) ([]OutboxMessage, error)
	// RecoverStuckClaims resets PROCESSING rows older than stuckClaimTimeout back to PENDING on default DB.
	RecoverStuckClaims(ctx context.Context, eventType string) error
	// RecoverStuckClaimsFromDB resets PROCESSING rows on a specific target DB pool.
	RecoverStuckClaimsFromDB(ctx context.Context, targetDB *sql.DB, eventType string) error
	MarkPublished(ctx context.Context, id string) error
	MarkPublishedOnDB(ctx context.Context, targetDB *sql.DB, id string) error
	MarkFailed(ctx context.Context, id string, err error) error
	MarkFailedOnDB(ctx context.Context, targetDB *sql.DB, id string, err error) error
}

type postgresOutboxRepository struct {
	db *sql.DB
}

func NewOutboxRepository(db *sql.DB) OutboxRepository {
	return &postgresOutboxRepository{db: db}
}

func (r *postgresOutboxRepository) CreateOutboxMessage(ctx context.Context, tx *sql.Tx, msg OutboxMessage) error {
	const query = `
		INSERT INTO public.outbox (
			id, tenant_id, aggregate_type, aggregate_id, event_type, payload, status, retry_count
		) VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', 0);
	`
	if _, err := tx.ExecContext(ctx, query,
		msg.ID, msg.TenantID, msg.AggregateType, msg.AggregateID, msg.EventType, string(msg.Payload),
	); err != nil {
		return fmt.Errorf("failed to insert outbox message in transaction: %w", err)
	}
	return nil
}

func (r *postgresOutboxRepository) FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]OutboxMessage, error) {
	return r.FetchAndClaimBatchFromDB(ctx, r.db, eventType, limit)
}

func (r *postgresOutboxRepository) FetchAndClaimBatchFromDB(ctx context.Context, targetDB *sql.DB, eventType string, limit int) ([]OutboxMessage, error) {
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
	rows, err := targetDB.QueryContext(ctx, query, eventType, maxRetries, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch and claim outbox batch: %w", err)
	}
	defer rows.Close()

	var list []OutboxMessage
	for rows.Next() {
		var msg OutboxMessage
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
	return list, rows.Err()
}

func (r *postgresOutboxRepository) RecoverStuckClaims(ctx context.Context, eventType string) error {
	return r.RecoverStuckClaimsFromDB(ctx, r.db, eventType)
}

func (r *postgresOutboxRepository) RecoverStuckClaimsFromDB(ctx context.Context, targetDB *sql.DB, eventType string) error {
	const query = `
		UPDATE public.outbox
		SET status     = 'PENDING',
		    claimed_at = NULL
		WHERE status     = 'PROCESSING'
		  AND event_type = $1
		  AND claimed_at < NOW() - $2::interval;
	`
	_, err := targetDB.ExecContext(ctx, query, eventType, fmt.Sprintf("%d seconds", int(stuckClaimTimeout.Seconds())))
	if err != nil {
		return fmt.Errorf("failed to recover stuck claimed outbox rows: %w", err)
	}
	return nil
}

func (r *postgresOutboxRepository) MarkPublished(ctx context.Context, id string) error {
	return r.MarkPublishedOnDB(ctx, r.db, id)
}

func (r *postgresOutboxRepository) MarkPublishedOnDB(ctx context.Context, targetDB *sql.DB, id string) error {
	const query = `
		UPDATE public.outbox
		SET status       = 'PUBLISHED',
		    claimed_at   = NULL,
		    processed_at = NOW()
		WHERE id = $1;
	`
	if _, err := targetDB.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("failed to mark outbox record as published: %w", err)
	}
	return nil
}

func (r *postgresOutboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	return r.MarkFailedOnDB(ctx, r.db, id, err)
}

func (r *postgresOutboxRepository) MarkFailedOnDB(ctx context.Context, targetDB *sql.DB, id string, err error) error {
	safeErr := sanitizeError(err)
	const query = `
		UPDATE public.outbox
		SET retry_count   = retry_count + 1,
		    last_error    = $2,
		    claimed_at    = NULL,
		    next_retry_at = CASE
		        WHEN retry_count + 1 < $3
		        THEN NOW() + (INTERVAL '1 second' * POWER(2, retry_count + 1))
		        ELSE NULL
		    END,
		    status = CASE
		        WHEN retry_count + 1 >= $3 THEN 'FAILED'
		        ELSE 'PENDING'
		    END
		WHERE id = $1;
	`
	if _, dbErr := targetDB.ExecContext(ctx, query, id, safeErr, maxRetries); dbErr != nil {
		return fmt.Errorf("failed to mark outbox record as failed: %w", dbErr)
	}
	return nil
}
