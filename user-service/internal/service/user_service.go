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
	ListUsers(ctx context.Context, tenantID string) ([]domain.User, error)
	AddUserTenantMembership(ctx context.Context, userID, tenantID string) error
	UserBelongsToTenant(ctx context.Context, userID, tenantID string) (bool, error)
}

// OutboxRepository is the consumer-side interface expected by UserService.
type OutboxRepository interface {
	CreateOutboxMessage(ctx context.Context, input repository.CreateOutboxMessageInput) error
}

type UserService struct {
	userRepository   UserRepository
	outboxRepository OutboxRepository
}

func NewUserService(
	userRepository UserRepository,
	outboxRepository OutboxRepository,
) *UserService {
	return &UserService{
		userRepository:   userRepository,
		outboxRepository: outboxRepository,
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

	if err := userService.userRepository.AddUserTenantMembership(ctx, userID, input.TenantID); err != nil {
		return fmt.Errorf("user service: failed to add user tenant membership: %w", err)
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

func (userService *UserService) ListUsers(ctx context.Context, tenantID string) ([]domain.User, error) {
	if tenantID == "" {
		return nil, ErrTenantIDRequired
	}
	return userService.userRepository.ListUsers(ctx, tenantID)
}

func (userService *UserService) UserBelongsToTenant(ctx context.Context, userID, tenantID string) (bool, error) {
	if userID == "" {
		return false, ErrUserIDRequired
	}
	if tenantID == "" {
		return false, ErrTenantIDRequired
	}
	return userService.userRepository.UserBelongsToTenant(ctx, userID, tenantID)
}
