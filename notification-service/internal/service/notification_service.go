package service

import (
	"context"
	"fmt"
	"log"

	"notification-service/internal/mailer"
	"notification-service/internal/repository"
)

type SendWelcomeNotificationInput struct {
	EventID    string
	TenantID   string
	OwnerEmail string
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
	isDuplicate, err := s.inboxRepo.TryInsert(ctx, input.EventID)
	if err != nil {
		return fmt.Errorf("inbox guard failed: %w", err)
	}
	if isDuplicate {
		log.Printf("NotificationService: Duplicate event_id='%s' detected by Inbox guard. Skipping.", input.EventID)
		return nil
	}

	subject := "Welcome! Your Tenant Workspace is Ready"
	bodyText := fmt.Sprintf(
		"Hello,\n\nYour tenant workspace '%s' has been successfully provisioned and is ready for use.\n\nThank you for choosing our platform!",
		input.TenantID,
	)

	auditLog := repository.NotificationLog{
		TenantID:       input.TenantID,
		RecipientEmail: input.OwnerEmail,
		Subject:        subject,
		Body:           bodyText,
		Status:         "sent",
	}
	logID, dbErr := s.repo.CreateNotificationLog(ctx, auditLog)
	if dbErr != nil {
		return fmt.Errorf("failed to persist notification audit log: %w", dbErr)
	}

	log.Printf("NotificationService: Prepared notification for event_id='%s', audit log id=%d", input.EventID, logID)

	_, _, mailErr := s.mailer.SendWelcomeEmail(input.OwnerEmail, input.TenantID)
	if mailErr != nil {
		log.Printf("NotificationService: Failed to send welcome email to '%s': %v", input.OwnerEmail, mailErr)
		return mailErr
	}

	log.Printf("NotificationService: Welcome email dispatched to '%s' for tenant='%s'", input.OwnerEmail, input.TenantID)
	return nil
}
