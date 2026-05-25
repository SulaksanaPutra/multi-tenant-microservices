package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"notification-service/internal/domain"
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

type ProcessEventOutput struct {
	LogID          int
	UserID         string
	RecipientEmail string
	TenantID       string
	TenantName     string
	TenantSlug     string
	OwnerName      string
}

// NotificationRepository is the consumer-side interface expected by NotificationService.
type NotificationRepository interface {
	CreateNotificationLog(ctx context.Context, input repository.CreateNotificationLogInput) (int, error)
	UpdateNotificationStatus(ctx context.Context, id int, status string) error
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

func (s *NotificationService) UpdateNotificationStatus(ctx context.Context, logID int, status string) error {
	if logID <= 0 {
		return fmt.Errorf("invalid notification log id: %d", logID)
	}
	if strings.TrimSpace(status) == "" {
		return fmt.Errorf("status cannot be empty")
	}
	return s.notificationRepository.UpdateNotificationStatus(ctx, logID, status)
}

func (s *NotificationService) ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, domain.ErrTenantIDRequired
	}
	return s.notificationRepository.ListNotifications(ctx, tenantID)
}

func (s *NotificationService) ProcessEventAndTrySendWelcome(
	ctx context.Context,
	input ProcessEventInput,
	events []domain.InboxMessage,
) (*ProcessEventOutput, error) {
	if strings.TrimSpace(input.EventID) == "" {
		return nil, domain.ErrEventIDRequired
	}
	if strings.TrimSpace(input.TenantID) == "" {
		return nil, domain.ErrTenantIDRequired
	}

	var hasUserCreated, hasWorkspaceReady bool
	var userID, recipientEmail, tenantName, tenantSlug, ownerName string

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
				TenantName string `json:"tenant_name"`
				TenantSlug string `json:"tenant_slug"`
				OwnerName  string `json:"owner_name"`
			}
			if err := json.Unmarshal(evt.Payload, &wsEvt); err == nil {
				if recipientEmail == "" {
					recipientEmail = wsEvt.OwnerEmail
				}
				if tenantName == "" {
					tenantName = wsEvt.TenantName
				}
				if tenantSlug == "" {
					tenantSlug = wsEvt.TenantSlug
				}
				if ownerName == "" {
					ownerName = wsEvt.OwnerName
				}
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

	// Tenant display name: prefer the human-readable name carried on the
	// workspace.ready event; fall back to the opaque tenant ID for events
	// published before tenant info was added to the payload.
	displayName := tenantName
	if strings.TrimSpace(displayName) == "" {
		displayName = input.TenantID
	}

	subject := "Welcome! Your Tenant Workspace is Ready"
	if strings.TrimSpace(tenantName) != "" {
		subject = fmt.Sprintf("Welcome to %s!", tenantName)
	}
	bodyText := buildWelcomeBody(displayName, tenantSlug, ownerName)

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
		TenantName:     tenantName,
		TenantSlug:     tenantSlug,
		OwnerName:      ownerName,
	}, nil
}

// buildWelcomeBody renders the plain-text welcome message shown to the tenant
// owner. The display name is always human-readable (either the tenant name or a
// fallback to the tenant ID), and the greeting is personalized with the owner's
// name when available.
func buildWelcomeBody(displayName, tenantSlug, ownerName string) string {
	greeting := "Hello"
	if strings.TrimSpace(ownerName) != "" {
		greeting = fmt.Sprintf("Hello %s", ownerName)
	}

	body := fmt.Sprintf(
		"%s,\n\nGreat news — your workspace \"%s\" has been successfully provisioned and is ready for use.",
		greeting,
		displayName,
	)
	if strings.TrimSpace(tenantSlug) != "" {
		body += fmt.Sprintf("\nWorkspace slug: %s", tenantSlug)
	}
	body += "\n\nThank you for choosing our platform!"
	return body
}
