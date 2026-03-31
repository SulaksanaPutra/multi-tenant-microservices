package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/authclient"
	"user-service/internal/repository"
)

var (
	ErrTenantIDRequired = errors.New("user service: tenant_id is required")
	ErrEmailRequired    = errors.New("user service: owner_email is required")
	ErrUserIDRequired   = errors.New("user service: user_id is required")
	ErrUserNameRequired = errors.New("user service: name is required")
)

type CreateUserFromWorkspaceInput struct {
	EventID    string
	TenantID   string
	OwnerEmail string
	OwnerName  string
}

type UpdateUserServiceInput struct {
	UserID string
	Name   string
}

// UserRepository is the consumer-side interface expected by UserService.
type UserRepository interface {
	CreateUser(ctx context.Context, input repository.CreateUserInput) error
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	UpdateUser(ctx context.Context, input repository.UpdateUserInput) error
	GetUserByID(ctx context.Context, userID string) (*domain.User, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
}

// OutboxRepository is the consumer-side interface expected by UserService.
type OutboxRepository interface {
	CreateOutboxMessage(ctx context.Context, input repository.CreateOutboxMessageInput) error
}

// RoleClient is the consumer-side interface expected by UserService for RBAC operations.
type RoleClient interface {
	AssignUserRole(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error)
	GetUserRole(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error)
	CreateRole(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error)
	ListRoles(ctx context.Context, authToken string) ([]authclient.Role, error)
	ListPermissions(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error)
}

type UserService struct {
	userRepository   UserRepository
	outboxRepository OutboxRepository
	roleClient       RoleClient
}

func NewUserService(
	userRepository UserRepository,
	outboxRepository OutboxRepository,
	roleClient RoleClient,
) *UserService {
	return &UserService{
		userRepository:   userRepository,
		outboxRepository: outboxRepository,
		roleClient:       roleClient,
	}
}

func (userService *UserService) CreateUserFromWorkspace(ctx context.Context, input CreateUserFromWorkspaceInput) error {
	if input.TenantID == "" {
		return ErrTenantIDRequired
	}
	if input.OwnerEmail == "" {
		return ErrEmailRequired
	}

	existingUser, err := userService.userRepository.GetUserByEmail(ctx, input.OwnerEmail)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("user service: failed to check existing user by email: %w", err)
	}

	var userID string
	if existingUser != nil {
		userID = existingUser.ID
	} else {
		userID = domain.GenerateUserID()
		userInput := repository.CreateUserInput{
			ID:    userID,
			Email: input.OwnerEmail,
			Name:  input.OwnerName,
		}

		if err := userService.userRepository.CreateUser(ctx, userInput); err != nil {
			return fmt.Errorf("user service: failed to create user: %w", err)
		}
	}

	if userService.outboxRepository != nil {
		outboxEventID := uuid.New().String()
		userCreatedEvt := domain.UserCreatedEvent{
			EventID:   outboxEventID,
			UserID:    userID,
			TenantID:  input.TenantID,
			Email:     input.OwnerEmail,
			Name:      input.OwnerName,
			CreatedAt: time.Now().UTC(),
		}
		payloadBytes, err := json.Marshal(userCreatedEvt)
		if err != nil {
			return fmt.Errorf("user service: failed to marshal UserCreated event: %w", err)
		}

		outboxInput := repository.CreateOutboxMessageInput{
			ID:            outboxEventID,
			TenantID:      input.TenantID,
			AggregateType: "User",
			AggregateID:   userID,
			EventType:     domain.RoutingKeyUserCreated,
			Payload:       payloadBytes,
		}

		if err := userService.outboxRepository.CreateOutboxMessage(ctx, outboxInput); err != nil {
			return fmt.Errorf("user service: failed to create user outbox message: %w", err)
		}
	}

	log.Printf("UserService: Successfully created owner user_id='%s' for tenant_id='%s' (email='%s') + written to Outbox",
		userID, input.TenantID, input.OwnerEmail)
	return nil
}

func (userService *UserService) UpdateUser(ctx context.Context, input UpdateUserServiceInput) error {
	if input.UserID == "" {
		return ErrUserIDRequired
	}
	if input.Name == "" {
		return ErrUserNameRequired
	}
	return userService.userRepository.UpdateUser(ctx, repository.UpdateUserInput{
		ID:   input.UserID,
		Name: input.Name,
	})
}

func (userService *UserService) GetUserByID(ctx context.Context, userID string) (*domain.User, error) {
	if userID == "" {
		return nil, ErrUserIDRequired
	}
	return userService.userRepository.GetUserByID(ctx, userID)
}

func (userService *UserService) ListUsers(ctx context.Context) ([]domain.User, error) {
	return userService.userRepository.ListUsers(ctx)
}

func (userService *UserService) AssignUserRole(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error) {
	if userService.roleClient == nil {
		return nil, fmt.Errorf("user service: role client not configured")
	}
	return userService.roleClient.AssignUserRole(ctx, authToken, userID, roleID)
}

func (userService *UserService) GetUserRole(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error) {
	if userService.roleClient == nil {
		return nil, fmt.Errorf("user service: role client not configured")
	}
	return userService.roleClient.GetUserRole(ctx, authToken, userID)
}

func (userService *UserService) CreateRole(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error) {
	if userService.roleClient == nil {
		return nil, fmt.Errorf("user service: role client not configured")
	}
	return userService.roleClient.CreateRole(ctx, authToken, input)
}

func (userService *UserService) ListRoles(ctx context.Context, authToken string) ([]authclient.Role, error) {
	if userService.roleClient == nil {
		return nil, fmt.Errorf("user service: role client not configured")
	}
	return userService.roleClient.ListRoles(ctx, authToken)
}

func (userService *UserService) ListPermissions(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error) {
	if userService.roleClient == nil {
		return nil, fmt.Errorf("user service: role client not configured")
	}
	return userService.roleClient.ListPermissions(ctx, authToken)
}
