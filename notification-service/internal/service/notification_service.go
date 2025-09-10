package service

import (
	"context"
	"fmt"
	"log"

	"notification-service/internal/mailer"
	"notification-service/internal/repository"
)

type SendWelcomeNotificationInput struct {
	EventID  string
	UserID   string
	TenantID string
}

type NotificationService interface {
	SendWelcomeNotification(ctx context.Context, input SendWelcomeNotificationInput) error
}

type notificationService struct {
	repo      repository.NotificationRepository
	inboxRepo repository.InboxRepository
	mailer    *mailer.Mailer
}

func NewNotificationService(
	repo repository.NotificationRepository,
	inboxRepo repository.InboxRepository,
	mailer *mailer.Mailer,
) NotificationService {
	return &notificationService{
		repo:      repo,
		inboxRepo: inboxRepo,
		mailer:    mailer,
	}
}

func (s *notificationService) SendWelcomeNotification(ctx context.Context, input SendWelcomeNotificationInput) error {
	// 1. Inbox Pattern Guard (uses transaction from ctx passed by consumer)
	isDuplicate, err := s.inboxRepo.TryInsert(ctx, input.EventID)
	if err != nil {
		return fmt.Errorf("inbox guard failed: %w", err)
	}
	if isDuplicate {
		log.Printf("NotificationService: Duplicate event_id='%s' detected by Inbox guard. Skipping.", input.EventID)
		return nil
	}

	// 2. Fetch recipient email
	userEmail, err := s.repo.GetUserEmailByID(ctx, input.UserID)
	if err != nil {
		return fmt.Errorf("failed to fetch user email for notification: %w", err)
	}

	// 3. Persist Notification Audit Log (shares same transaction via ctx)
	subject := "Welcome! Your Tenant Workspace is Ready"
	bodyText := fmt.Sprintf(
		"Hello,\n\nYour tenant workspace '%s' has been successfully provisioned and is ready for use.\n\nThank you for choosing our platform!",
		input.TenantID,
	)

	auditLog := repository.NotificationLog{
		UserID:         input.UserID,
		TenantID:       input.TenantID,
		RecipientEmail: userEmail,
		Subject:        subject,
		Body:           bodyText,
		Status:         "sent",
	}
	logID, dbErr := s.repo.CreateNotificationLog(ctx, auditLog)
	if dbErr != nil {
		return fmt.Errorf("failed to persist notification audit log: %w", dbErr)
	}

	log.Printf("NotificationService: Prepared notification for event_id='%s', audit log id=%d", input.EventID, logID)

	// 4. Send email via Mailer
	_, _, mailErr := s.mailer.SendWelcomeEmail(userEmail, input.TenantID)
	if mailErr != nil {
		log.Printf("NotificationService: Failed to send welcome email to '%s': %v", userEmail, mailErr)
		return mailErr
	}

	log.Printf("NotificationService: Welcome email dispatched to '%s' for tenant='%s'", userEmail, input.TenantID)
	return nil
}
