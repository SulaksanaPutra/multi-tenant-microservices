package repository

import (
	"context"
	"database/sql"
	"fmt"

	"notification-service/internal/infrastructure/postgres"
)

type NotificationLog struct {
	UserID         string
	TenantID       string
	RecipientEmail string
	Subject        string
	Body           string
	Status         string
}

type NotificationRepository interface {
	GetUserEmailByID(ctx context.Context, userID string) (string, error)
	// CreateNotificationLog inserts an audit log row outside a transaction.
	CreateNotificationLog(ctx context.Context, log NotificationLog) (int, error)
	// CreateNotificationLogTx inserts an audit log row inside an existing transaction.
	// Used by the Inbox Pattern flow to ensure the log write and inbox INSERT
	// either both commit or both rollback together.
	CreateNotificationLogTx(ctx context.Context, tx *sql.Tx, log NotificationLog) (int, error)
}

type postgresNotificationRepository struct {
	client *postgres.Client
}

func NewNotificationRepository(client *postgres.Client) NotificationRepository {
	return &postgresNotificationRepository{client: client}
}

func (r *postgresNotificationRepository) GetUserEmailByID(ctx context.Context, userID string) (string, error) {
	query := `SELECT email FROM public.users WHERE id = $1;`
	var email string
	err := r.client.QueryRowContext(ctx, query, userID).Scan(&email)
	if err != nil {
		return "", fmt.Errorf("user not found for id '%s': %w", userID, err)
	}
	return email, nil
}

func (r *postgresNotificationRepository) CreateNotificationLog(ctx context.Context, log NotificationLog) (int, error) {
	query := `
		INSERT INTO public.notifications (user_id, tenant_id, recipient_email, subject, body, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id;
	`
	var id int
	err := r.client.QueryRowContext(ctx, query, log.UserID, log.TenantID, log.RecipientEmail, log.Subject, log.Body, log.Status).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("failed to insert notification log: %w", err)
	}
	return id, nil
}

// CreateNotificationLogTx inserts an audit row inside an existing *sql.Tx.
// This is called inside the Inbox Pattern flow so that the log write shares
// the same transaction as the inbox INSERT, guaranteeing atomicity.
func (r *postgresNotificationRepository) CreateNotificationLogTx(ctx context.Context, tx *sql.Tx, log NotificationLog) (int, error) {
	query := `
		INSERT INTO public.notifications (user_id, tenant_id, recipient_email, subject, body, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id;
	`
	var id int
	err := tx.QueryRowContext(ctx, query, log.UserID, log.TenantID, log.RecipientEmail, log.Subject, log.Body, log.Status).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("failed to insert notification log in transaction: %w", err)
	}
	return id, nil
}
