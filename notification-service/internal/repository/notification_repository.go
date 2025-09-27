package repository

import (
	"context"
	"fmt"

	"notification-service/internal/infrastructure/postgres"
	"notification-service/internal/txctx"
)

type NotificationLog struct {
	TenantID       string
	RecipientEmail string
	Subject        string
	Body           string
	Status         string
}

type NotificationRepository interface {
	CreateNotificationLog(ctx context.Context, log NotificationLog) (int, error)
	HasSentNotification(ctx context.Context, tenantID string) (bool, error)
}

type notificationRepository struct {
	client *postgres.Client
}

func NewNotificationRepository(client *postgres.Client) NotificationRepository {
	return &notificationRepository{client: client}
}

func (r *notificationRepository) CreateNotificationLog(ctx context.Context, log NotificationLog) (int, error) {
	exec := txctx.GetExecutor(ctx, r.client.DB)
	query := `
		INSERT INTO public.notifications (tenant_id, recipient_email, subject, body, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id;
	`
	var id int
	err := exec.QueryRowContext(ctx, query, log.TenantID, log.RecipientEmail, log.Subject, log.Body, log.Status).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("failed to insert notification log: %w", err)
	}
	return id, nil
}

func (r *notificationRepository) HasSentNotification(ctx context.Context, tenantID string) (bool, error) {
	exec := txctx.GetExecutor(ctx, r.client.DB)
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
