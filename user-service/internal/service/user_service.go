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

type CreateUserFromWorkspaceInput struct {
	EventID    string
	TenantID   string
	OwnerEmail string
	OwnerName  string
}

type UpdateUserInput struct {
	UserID string
	Name   string
}

type UserOutput struct {
	ID        string
	Email     string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func toUserOutput(u domain.User) UserOutput {
	return UserOutput{
		ID:        u.ID,
		Email:     u.Email,
		Name:      u.Name,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}

type UserRepository interface {
	CreateUser(ctx context.Context, input repository.CreateUserInput) error
	GetUserByEmail(ctx context.Context, email string) (*domain.User, error)
	GetUserByID(ctx context.Context, userID string) (*domain.User, error)
	UpdateUser(ctx context.Context, input repository.UpdateUserInput) error
	ListUsers(ctx context.Context, tenantID string) ([]domain.User, error)
	AddUserTenantMembership(ctx context.Context, userID, tenantID string) error
}

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
		return domain.ErrTenantIDRequired
	}
	if input.OwnerEmail == "" {
		return domain.ErrEmailRequired
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

func (userService *UserService) UpdateUser(ctx context.Context, input UpdateUserInput) error {
	if input.UserID == "" {
		return domain.ErrUserIDRequired
	}
	if input.Name == "" {
		return domain.ErrUserNameRequired
	}
	return userService.userRepository.UpdateUser(ctx, repository.UpdateUserInput{
		ID:   input.UserID,
		Name: input.Name,
	})
}

func (userService *UserService) ListUsers(ctx context.Context, tenantID string) ([]UserOutput, error) {
	if tenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	users, err := userService.userRepository.ListUsers(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	outputs := make([]UserOutput, len(users))
	for i, u := range users {
		outputs[i] = toUserOutput(u)
	}
	return outputs, nil
}

func (userService *UserService) GetUserByID(ctx context.Context, userID string) (*UserOutput, error) {
	if userID == "" {
		return nil, domain.ErrUserIDRequired
	}
	user, err := userService.userRepository.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	output := toUserOutput(*user)
	return &output, nil
}
