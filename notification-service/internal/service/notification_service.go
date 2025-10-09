package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"notification-service/internal/mailer"
	"notification-service/internal/repository"
)

type ProcessEventInput struct {
	EventID    string
	UserID     string
	TenantID   string
	EventType  string
	OwnerEmail string
	Payload    []byte
}

type NotificationService interface {
	ProcessEventAndTrySendWelcome(ctx context.Context, input ProcessEventInput) error
	GetNotifications(ctx context.Context, tenantID string) ([]repository.NotificationLog, error)
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

func (s *notificationService) GetNotifications(ctx context.Context, tenantID string) ([]repository.NotificationLog, error) {
	return s.repo.GetNotifications(ctx, tenantID)
}

func (s *notificationService) ProcessEventAndTrySendWelcome(ctx context.Context, input ProcessEventInput) error {
	inboxMsg := repository.InboxMessage{
		EventID:   input.EventID,
		TenantID:  input.TenantID,
		EventType: input.EventType,
		Payload:   input.Payload,
	}

	isDup, err := s.inboxRepo.TryInsert(ctx, inboxMsg)
	if err != nil {
		return fmt.Errorf("inbox guard failed: %w", err)
	}
	if isDup {
		log.Printf("NotificationService: Duplicate event_id='%s' detected by Inbox guard. Skipping.", input.EventID)
		return nil
	}

	events, err := s.inboxRepo.GetEventsByTenantID(ctx, input.TenantID)
	if err != nil {
		return fmt.Errorf("failed to fetch inbox events for tenant_id='%s': %w", input.TenantID, err)
	}
	var hasUserCreated, hasWorkspaceReady bool
	var userID, recipientEmail string

	for _, evt := range events {
		if evt.EventType == "user.created" {
			hasUserCreated = true
			var userEvt struct {
				UserID string `json:"user_id"`
				Email  string `json:"email"`
			}
			if err := json.Unmarshal(evt.Payload, &userEvt); err == nil {
				if userEvt.UserID != "" {
					userID = userEvt.UserID
				}
				if userEvt.Email != "" {
					recipientEmail = userEvt.Email
				}
			}
		} else if evt.EventType == "workspace.ready" {
			hasWorkspaceReady = true
			var wsEvt struct {
				OwnerEmail string `json:"owner_email"`
			}
			if err := json.Unmarshal(evt.Payload, &wsEvt); err == nil && recipientEmail == "" {
				recipientEmail = wsEvt.OwnerEmail
			}
		}
	}

	if userID == "" {
		userID = input.UserID
	}
	if userID == "" {
		userID = "usr_unknown"
	}
	if recipientEmail == "" {
		recipientEmail = input.OwnerEmail
	}
	if recipientEmail == "" {
		recipientEmail = "owner@tenant.com"
	}

	if !hasUserCreated || !hasWorkspaceReady {
		log.Printf("NotificationService: Tenant_id='%s' recorded '%s' event, but barrier condition not met yet (user_created=%v, workspace_ready=%v). Waiting...",
			input.TenantID, input.EventType, hasUserCreated, hasWorkspaceReady)
		return nil
	}

	alreadySent, err := s.repo.HasSentNotification(ctx, input.TenantID)
	if err != nil {
		return fmt.Errorf("failed checking welcome email sent status for tenant_id='%s': %w", input.TenantID, err)
	}
	if alreadySent {
		log.Printf("NotificationService: Both barrier events present for tenant_id='%s', but welcome email was already dispatched. Skipping.", input.TenantID)
		return nil
	}

	subject := "Welcome! Your Tenant Workspace is Ready"
	bodyText := fmt.Sprintf(
		"Hello,\n\nYour tenant workspace '%s' has been successfully provisioned and is ready for use.\n\nThank you for choosing our platform!",
		input.TenantID,
	)

	auditLog := repository.NotificationLog{
		UserID:         userID,
		TenantID:       input.TenantID,
		RecipientEmail: recipientEmail,
		Subject:        subject,
		Body:           bodyText,
		Status:         "sent",
	}
	logID, dbErr := s.repo.CreateNotificationLog(ctx, auditLog)
	if dbErr != nil {
		return fmt.Errorf("failed to persist notification audit log: %w", dbErr)
	}

	log.Printf("NotificationService: Barrier condition met! Prepared notification for event_id='%s', audit log id=%d", input.EventID, logID)

	_, _, mailErr := s.mailer.SendWelcomeEmail(recipientEmail, input.TenantID)
	if mailErr != nil {
		log.Printf("NotificationService: Failed to send welcome email to '%s': %v", recipientEmail, mailErr)
		return mailErr
	}

	log.Printf("NotificationService: Welcome email successfully dispatched to '%s' for tenant='%s'", recipientEmail, input.TenantID)
	return nil
}
