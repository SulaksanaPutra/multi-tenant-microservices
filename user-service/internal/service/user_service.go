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
	"user-service/internal/publisher"
	"user-service/internal/repository"
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

// UserRepo is the consumer-side interface expected by UserService.
type UserRepo interface {
	CreateUser(ctx context.Context, user domain.User) error
}

// OutboxRepo is the consumer-side interface expected by UserService.
type OutboxRepo interface {
	CreateOutboxMessage(ctx context.Context, msg repository.OutboxMessage) error
}

type UserService struct {
	userRepo   UserRepo
	outboxRepo OutboxRepo
}

func NewUserService(
	userRepo UserRepo,
	outboxRepo OutboxRepo,
) *UserService {
	return &UserService{
		userRepo:   userRepo,
		outboxRepo: outboxRepo,
	}
}

func (s *UserService) CreateUserFromWorkspace(ctx context.Context, input CreateUserFromWorkspaceInput) error {
	userID := domain.GenerateUserID()
	userObj := domain.User{
		ID:    userID,
		Email: input.OwnerEmail,
		Name:  input.OwnerName,
	}

	if err := s.userRepo.CreateUser(ctx, userObj); err != nil {
		return fmt.Errorf("failed to create user: %w", err)
	}

	if s.outboxRepo != nil {
		outboxEventID := uuid.New().String()
		userCreatedEvt := publisher.UserCreatedEvent{
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

		outboxMsg := repository.OutboxMessage{
			ID:            outboxEventID,
			TenantID:      &input.TenantID,
			AggregateType: "User",
			AggregateID:   userID,
			EventType:     publisher.RoutingKeyUserCreated,
			Payload:       payloadBytes,
		}

		if err := s.outboxRepo.CreateOutboxMessage(ctx, outboxMsg); err != nil {
			return fmt.Errorf("failed to create user outbox message: %w", err)
		}
	}

	log.Printf("UserService: Successfully created owner user_id='%s' for tenant_id='%s' (email='%s') + written to Outbox",
		userID, input.TenantID, input.OwnerEmail)
	return nil
}

