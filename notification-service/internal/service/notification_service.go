package service

import (
	"context"
	"fmt"
	"log"

	"notification-service/internal/mailer"
	"notification-service/internal/repository"
)

type SendWelcomeNotificationInput struct {
	UserID   string
	TenantID string
}

type NotificationService interface {
	SendWelcomeNotification(ctx context.Context, input SendWelcomeNotificationInput) error
}

type notificationService struct {
	repo   repository.NotificationRepository
	mailer *mailer.Mailer
}

func NewNotificationService(repo repository.NotificationRepository, mailer *mailer.Mailer) NotificationService {
	return &notificationService{
		repo:   repo,
		mailer: mailer,
	}
}

func (s *notificationService) SendWelcomeNotification(ctx context.Context, input SendWelcomeNotificationInput) error {
	// 1. Query recipient user email from repository
	userEmail, err := s.repo.GetUserEmailByID(ctx, input.UserID)
	if err != nil {
		return fmt.Errorf("failed to fetch user email for notification: %w", err)
	}

	// 2. Dispatch Welcome Email via Mailer driver
	subject, bodyText, err := s.mailer.SendWelcomeEmail(userEmail, input.TenantID)
	status := "sent"
	if err != nil {
		log.Printf("NotificationService Error sending email to %s: %v", userEmail, err)
		status = "failed"
	}

	// 3. Persist Notification Audit Log via NotificationRepository
	auditLog := repository.NotificationLog{
		UserID:         input.UserID,
		TenantID:       input.TenantID,
		RecipientEmail: userEmail,
		Subject:        subject,
		Body:           bodyText,
		Status:         status,
	}

	logID, dbErr := s.repo.CreateNotificationLog(ctx, auditLog)
	if dbErr != nil {
		log.Printf("NotificationService Warning: Failed to insert audit log: %v", dbErr)
	} else {
		log.Printf("NotificationService: Inserted audit log row id=%d (status=%s)", logID, status)
	}

	return err
}
