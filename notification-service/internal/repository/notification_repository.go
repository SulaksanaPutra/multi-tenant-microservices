package repository

import (
	"context"
	"fmt"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/txcontext"
)

type CreateNotificationLogInput struct {
	UserID      string
	TenantID    string
	Description string
	Body        string
	Status      string
}

type NotificationRepository struct {
	dbClient *postgres.Client
}

func NewNotificationRepository(dbClient *postgres.Client) *NotificationRepository {
	return &NotificationRepository{dbClient: dbClient}
}

func (r *NotificationRepository) CreateNotificationLog(ctx context.Context, input CreateNotificationLogInput) (string, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	id := domain.GenerateNotificationID()
	query := `
		INSERT INTO public.notifications (id, user_id, tenant_id, description, body, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id;
	`
	var insertedID string
	err := exec.QueryRowContext(ctx, query, id, input.UserID, input.TenantID, input.Description, input.Body, input.Status).Scan(&insertedID)
	if err != nil {
		return "", fmt.Errorf("failed to insert notification log: %w", err)
	}
	return insertedID, nil
}

func (r *NotificationRepository) UpdateNotificationStatus(ctx context.Context, id string, status string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		UPDATE public.notifications
		SET status = $1, updated_at = NOW()
		WHERE id = $2;
	`
	res, err := exec.ExecContext(ctx, query, status, id)
	if err != nil {
		return fmt.Errorf("failed to update notification status for id=%s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for notification status update id=%s: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("notification log with id=%s not found for status update", id)
	}
	return nil
}

func (r *NotificationRepository) HasSentNotification(ctx context.Context, tenantID string) (bool, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		SELECT COUNT(1)
		FROM public.notifications
		WHERE tenant_id = $1 AND status = 'sent';
	`
	var count int
	if err := exec.QueryRowContext(ctx, query, tenantID).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to check notification status for tenant_id='%s': %w", tenantID, err)
	}
	return count > 0, nil
}

func (r *NotificationRepository) ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	var query string
	var args []interface{}

	if tenantID != "" {
		query = `
			SELECT id, user_id, tenant_id, description, body, status, created_at, updated_at
			FROM public.notifications
			WHERE tenant_id = $1
			ORDER BY created_at DESC;
		`
		args = append(args, tenantID)
	} else {
		query = `
			SELECT id, user_id, tenant_id, description, body, status, created_at, updated_at
			FROM public.notifications
			ORDER BY created_at DESC
			LIMIT 50;
		`
	}

	rows, err := exec.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query notifications: %w", err)
	}
	defer rows.Close()

	var logs []domain.NotificationLog
	for rows.Next() {
		var l domain.NotificationLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.TenantID, &l.Description, &l.Body, &l.Status, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan notification row: %w", err)
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []domain.NotificationLog{}
	}
	return logs, nil
}
