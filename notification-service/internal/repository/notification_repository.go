package repository

import (
	"context"
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
	CreateNotificationLog(ctx context.Context, log NotificationLog) (int, error)
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
