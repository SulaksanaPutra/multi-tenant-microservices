package repository

import (
	"context"
	"fmt"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/txcontext"
)

type NotificationRepository struct {
	client *postgres.Client
}

func NewNotificationRepository(client *postgres.Client) *NotificationRepository {
	return &NotificationRepository{client: client}
}

func (r *NotificationRepository) CreateNotificationLog(ctx context.Context, log domain.NotificationLog) (int, error) {
	exec := txcontext.GetExecutor(ctx, r.client.DB)
	query := `
		INSERT INTO public.notifications (user_id, tenant_id, recipient_email, subject, body, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id;
	`
	var id int
	err := exec.QueryRowContext(ctx, query, log.UserID, log.TenantID, log.RecipientEmail, log.Subject, log.Body, log.Status).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("failed to insert notification log: %w", err)
	}
	return id, nil
}

func (r *NotificationRepository) HasSentNotification(ctx context.Context, tenantID string) (bool, error) {
	exec := txcontext.GetExecutor(ctx, r.client.DB)
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
	exec := txcontext.GetExecutor(ctx, r.client.DB)
	var query string
	var args []interface{}

	if tenantID != "" {
		query = `
			SELECT id, user_id, tenant_id, recipient_email, subject, body, status, created_at
			FROM public.notifications
			WHERE tenant_id = $1
			ORDER BY created_at DESC;
		`
		args = append(args, tenantID)
	} else {
		query = `
			SELECT id, user_id, tenant_id, recipient_email, subject, body, status, created_at
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
		if err := rows.Scan(&l.ID, &l.UserID, &l.TenantID, &l.RecipientEmail, &l.Subject, &l.Body, &l.Status, &l.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan notification row: %w", err)
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []domain.NotificationLog{}
	}
	return logs, nil
}
