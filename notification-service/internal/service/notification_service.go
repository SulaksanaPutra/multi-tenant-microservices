package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"notification-service/internal/domain"
	"notification-service/internal/repository"
)

var (
	ErrTenantIDRequired = errors.New("notification service: tenant_id is required")
	ErrEventIDRequired  = errors.New("notification service: event_id is required")
)

type ProcessEventInput struct {
	EventID    string
	UserID     string
	TenantID   string
	EventType  string
	OwnerEmail string
	Payload    []byte
}

type ProcessEventOutput struct {
	LogID          int
	UserID         string
	RecipientEmail string
	TenantID       string
}

// NotificationRepository is the consumer-side interface expected by NotificationService.
type NotificationRepository interface {
	CreateNotificationLog(ctx context.Context, input repository.CreateNotificationLogInput) (int, error)
	HasSentNotification(ctx context.Context, tenantID string) (bool, error)
	ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error)
}

type NotificationService struct {
	notificationRepository NotificationRepository
}

func NewNotificationService(
	notificationRepository NotificationRepository,
) *NotificationService {
	return &NotificationService{
		notificationRepository: notificationRepository,
	}
}

func (s *NotificationService) ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrTenantIDRequired
	}
	return s.notificationRepository.ListNotifications(ctx, tenantID)
}

func (s *NotificationService) ProcessEventAndTrySendWelcome(
	ctx context.Context,
	input ProcessEventInput,
	events []domain.InboxMessage,
) (*ProcessEventOutput, error) {
	if strings.TrimSpace(input.EventID) == "" {
		return nil, ErrEventIDRequired
	}
	if strings.TrimSpace(input.TenantID) == "" {
		return nil, ErrTenantIDRequired
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
		return nil, nil
	}

	alreadySent, err := s.notificationRepository.HasSentNotification(ctx, input.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed checking welcome email sent status for tenant_id='%s': %w", input.TenantID, err)
	}
	if alreadySent {
		log.Printf("NotificationService: Both barrier events present for tenant_id='%s', but welcome email was already dispatched. Skipping.", input.TenantID)
		return nil, nil
	}

	subject := "Welcome! Your Tenant Workspace is Ready"
	bodyText := fmt.Sprintf(
		"Hello,\n\nYour tenant workspace '%s' has been successfully provisioned and is ready for use.\n\nThank you for choosing our platform!",
		input.TenantID,
	)

	// Write an audit log with the status "pending" inside the caller's transaction.
	// The consumer updates this to "sent" after the SMTP call succeeds post-commit.
	auditLogInput := repository.CreateNotificationLogInput{
		UserID:         userID,
		TenantID:       input.TenantID,
		RecipientEmail: recipientEmail,
		Subject:        subject,
		Body:           bodyText,
		Status:         "pending",
	}
	logID, dbErr := s.notificationRepository.CreateNotificationLog(ctx, auditLogInput)
	if dbErr != nil {
		return nil, fmt.Errorf("failed to persist notification audit log: %w", dbErr)
	}

	log.Printf("NotificationService: Barrier metdomain — persisted pending notification log id=%d for event_id='%s'", logID, input.EventID)

	return &ProcessEventOutput{
		LogID:          logID,
		UserID:         userID,
		RecipientEmail: recipientEmail,
		TenantID:       input.TenantID,
	}, nil
}
