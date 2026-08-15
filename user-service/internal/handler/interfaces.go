package handler

import (
	"context"

	"user-service/internal/service"
)

// =============================================================================
// Domain Service Contracts
// =============================================================================

// UserService is the handler-side interface for user profile management.
type UserService interface {
	ListUsers(ctx context.Context, tenantID string) ([]service.UserOutput, error)
	GetUserByID(ctx context.Context, userID string) (*service.UserOutput, error)
	UpdateUser(ctx context.Context, input service.UpdateUserInput) error
}
