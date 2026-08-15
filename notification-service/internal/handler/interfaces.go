package handler

import (
	"context"

	"notification-service/internal/service"
)

// =============================================================================
// Domain Service Contracts
// =============================================================================

// NotificationService is the handler-side interface for notification queries.
type NotificationService interface {
	ListNotifications(ctx context.Context, tenantID string) ([]service.NotificationLogOutput, error)
}
