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
)

var (
	ErrTenantIDRequired = errors.New("user service: tenant_id is required")
	ErrEmailRequired    = errors.New("user service: owner_email is required")
)

type CreateUserFromWorkspaceInput struct {
	EventID    string
	TenantID   string
	OwnerEmail string
	OwnerName  string
}

// UserRepository is the consumer-side interface expected by UserService.
type UserRepository interface {
	CreateUser(ctx context.Context, user domain.User) error
}

// OutboxRepository is the consumer-side interface expected by UserService.
type OutboxRepository interface {
	CreateOutboxMessage(ctx context.Context, msg domain.OutboxMessage) error
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

func (s *UserService) CreateUserFromWorkspace(ctx context.Context, input CreateUserFromWorkspaceInput) error {
	if input.TenantID == "" {
		return ErrTenantIDRequired
	}
	if input.OwnerEmail == "" {
		return ErrEmailRequired
	}

	userID := domain.GenerateUserID()
	userObj := domain.User{
		ID:    userID,
		Email: input.OwnerEmail,
		Name:  input.OwnerName,
	}

	if err := s.userRepository.CreateUser(ctx, userObj); err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	if s.outboxRepository != nil {
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
			return fmt.Errorf("failed to marshal UserCreated event: %w", err)
		}

		outboxMsg := domain.OutboxMessage{
			ID:            outboxEventID,
			TenantID:      &input.TenantID,
			AggregateType: "User",
			AggregateID:   userID,
			EventType:     domain.RoutingKeyUserCreated,
			Payload:       payloadBytes,
		}

		if err := s.outboxRepository.CreateOutboxMessage(ctx, outboxMsg); err != nil {
			return fmt.Errorf("failed to create user outbox message: %w", err)
		}
	}

	log.Printf("UserService: Successfully created owner user_id='%s' for tenant_id='%s' (email='%s') + written to Outbox",
		userID, input.TenantID, input.OwnerEmail)
	return nil
}

