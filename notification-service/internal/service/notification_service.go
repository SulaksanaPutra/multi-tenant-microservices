package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

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
	LogID          string
	UserID         string
	RecipientEmail string
	TenantID       string
	TenantName     string
	TenantSlug     string
	OwnerName      string
}

type NotificationLogOutput struct {
	ID          string
	UserID      string
	TenantID    string
	Description string
	Body        string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func toNotificationLogOutput(l domain.NotificationLog) NotificationLogOutput {
	return NotificationLogOutput{
		ID:          l.ID,
		UserID:      l.UserID,
		TenantID:    l.TenantID,
		Description: l.Description,
		Body:        l.Body,
		Status:      l.Status,
		CreatedAt:   l.CreatedAt,
		UpdatedAt:   l.UpdatedAt,
	}
}

type NotificationRepository interface {
	CreateNotificationLog(ctx context.Context, input repository.CreateNotificationLogInput) (string, error)
	UpdateNotificationStatus(ctx context.Context, id string, status string) error
	HasSentNotification(ctx context.Context, tenantID string) (bool, error)
	FindPendingNotification(ctx context.Context, tenantID string) (*domain.NotificationLog, error)
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

func (notificationService *NotificationService) HasSentNotification(ctx context.Context, tenantID string) (bool, error) {
	if strings.TrimSpace(tenantID) == "" {
		return false, domain.ErrTenantIDRequired
	}
	return notificationService.notificationRepository.HasSentNotification(ctx, tenantID)
}

func (notificationService *NotificationService) CreateOrderNotification(ctx context.Context, evt domain.OrderCreatedEvent) error {
	if strings.TrimSpace(evt.TenantID) == "" {
		return domain.ErrTenantIDRequired
	}

	userID := evt.CustomerID
	if strings.TrimSpace(userID) == "" {
		userID = "usr_unknown"
	}

	description := "Order created"
	if strings.TrimSpace(evt.OrderID) != "" {
		description = fmt.Sprintf("Order %s created", evt.OrderID)
	}

	body := buildOrderBody(evt)

	_, err := notificationService.notificationRepository.CreateNotificationLog(ctx, repository.CreateNotificationLogInput{
		UserID:      userID,
		TenantID:    evt.TenantID,
		Description: description,
		Body:        body,
		Status:      "sent",
	})
	if err != nil {
		return fmt.Errorf("failed to persist order notification audit log: %w", err)
	}

	return nil
}

func buildOrderBody(evt domain.OrderCreatedEvent) string {
	body := "A new order has been created."
	if strings.TrimSpace(evt.OrderID) != "" {
		body += fmt.Sprintf("\nOrder ID: %s", evt.OrderID)
	}
	if strings.TrimSpace(evt.CustomerID) != "" {
		body += fmt.Sprintf("\nCustomer ID: %s", evt.CustomerID)
	}
	body += fmt.Sprintf("\nAmount: %.2f", evt.Amount)
	if strings.TrimSpace(evt.Status) != "" {
		body += fmt.Sprintf("\nStatus: %s", evt.Status)
	}
	return body
}

func (notificationService *NotificationService) UpdateNotificationStatus(ctx context.Context, logID string, status string) error {
	if strings.TrimSpace(logID) == "" {
		return fmt.Errorf("invalid notification log id: %s", logID)
	}
	if strings.TrimSpace(status) == "" {
		return errors.New("status cannot be empty")
	}
	return notificationService.notificationRepository.UpdateNotificationStatus(ctx, logID, status)
}

func (notificationService *NotificationService) ListNotifications(ctx context.Context, tenantID string) ([]NotificationLogOutput, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, domain.ErrTenantIDRequired
	}
	logs, err := notificationService.notificationRepository.ListNotifications(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	outputs := make([]NotificationLogOutput, len(logs))
	for i, l := range logs {
		outputs[i] = toNotificationLogOutput(l)
	}
	return outputs, nil
}

func (notificationService *NotificationService) ProcessEventAndTrySendWelcome(
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
		switch evt.EventType {
		case "user.created":
			hasUserCreated = true
			var userEvt domain.UserCreatedEvent
			if err := json.Unmarshal(evt.Payload, &userEvt); err == nil {
				if userEvt.UserID != "" {
					userID = userEvt.UserID
				}
				if userEvt.Email != "" {
					recipientEmail = userEvt.Email
				}
			}
		case "workspace.ready":
			hasWorkspaceReady = true
			var wsEvt domain.WorkspaceReadyEvent
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

	alreadySent, err := notificationService.notificationRepository.HasSentNotification(ctx, input.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed checking welcome email sent status for tenant_id='%s': %w", input.TenantID, err)
	}
	if alreadySent {
		log.Printf("NotificationService: Both barrier events present for tenant_id='%s', but welcome email was already dispatched. Skipping.", input.TenantID)
		return nil, nil
	}

	existingPending, err := notificationService.notificationRepository.FindPendingNotification(ctx, input.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed checking pending notification for tenant_id='%s': %w", input.TenantID, err)
	}

	var logID string
	if existingPending != nil {
		logID = existingPending.ID
		log.Printf("NotificationService: Reusing existing pending notification log id=%s for tenant_id='%s'", logID, input.TenantID)
	} else {
		displayName := tenantName
		if strings.TrimSpace(displayName) == "" {
			displayName = input.TenantID
		}

		description := "Welcome! Your Tenant Workspace is Ready"
		if strings.TrimSpace(tenantName) != "" {
			description = fmt.Sprintf("Welcome to %s!", tenantName)
		}
		bodyText := buildWelcomeBody(displayName, tenantSlug, ownerName)

		auditLogInput := repository.CreateNotificationLogInput{
			UserID:      userID,
			TenantID:    input.TenantID,
			Description: description,
			Body:        bodyText,
			Status:      "pending",
		}
		newID, dbErr := notificationService.notificationRepository.CreateNotificationLog(ctx, auditLogInput)
		if dbErr != nil {
			return nil, fmt.Errorf("failed to persist notification audit log: %w", dbErr)
		}
		logID = newID
		log.Printf("NotificationService: Barrier met — persisted pending notification log id=%s for event_id='%s'", logID, input.EventID)
	}

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
