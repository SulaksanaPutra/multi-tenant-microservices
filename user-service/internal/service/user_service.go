package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"user-service/internal/domain"
	"user-service/internal/publisher"
	"user-service/internal/repository"
)

type CreateUserFromWorkspaceInput struct {
	EventID    string
	TenantID   string
	OwnerEmail string
	OwnerName  string
}

type UserService interface {
	CreateUserFromWorkspace(ctx context.Context, input CreateUserFromWorkspaceInput) error
}

type userService struct {
	userRepo   repository.UserRepository
	inboxRepo  repository.InboxRepository
	outboxRepo repository.OutboxRepository
}

func NewUserService(
	userRepo repository.UserRepository,
	inboxRepo repository.InboxRepository,
	outboxRepo repository.OutboxRepository,
) UserService {
	return &userService{
		userRepo:   userRepo,
		inboxRepo:  inboxRepo,
		outboxRepo: outboxRepo,
	}
}

func (s *userService) CreateUserFromWorkspace(ctx context.Context, input CreateUserFromWorkspaceInput) error {
	if input.EventID != "" && s.inboxRepo != nil {
		isDup, err := s.inboxRepo.TryInsert(ctx, input.EventID)
		if err != nil {
			return fmt.Errorf("inbox guard failed: %w", err)
		}
		if isDup {
			log.Printf("UserService: Duplicate event_id='%s' detected by Inbox guard. Skipping.", input.EventID)
			return nil
		}
	}

	userID := domain.GenerateUserID()
	userObj := repository.User{
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

