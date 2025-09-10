package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"user-service/internal/publisher"
	"user-service/internal/repository"
	"user-service/internal/utils"
	"user-service/internal/worker"
)

type RegisterUserInput struct {
	Email      string
	Name       string
	TenantName string
	TenantSlug string
}

type RegisterUserOutput struct {
	UserID   string
	TenantID string
}

type HandleTenantProvisionedInput struct {
	EventID       string
	TenantID      string
	TenantSlug    string
	UserID        string
	PlacementType string
	SchemaName    string
	DbDSN         string
}

type UserService interface {
	RegisterUser(ctx context.Context, input RegisterUserInput) (*RegisterUserOutput, error)
	HandleTenantProvisioned(ctx context.Context, input HandleTenantProvisionedInput) error
}

type userService struct {
	userRepo     repository.UserRepository
	tenantRepo   repository.TenantRepository
	outboxRepo   repository.OutboxRepository
	inboxRepo    repository.InboxRepository
	outboxWorker *worker.OutboxWorker
}

type UserServiceParams struct {
	UserRepo     repository.UserRepository
	TenantRepo   repository.TenantRepository
	OutboxRepo   repository.OutboxRepository
	InboxRepo    repository.InboxRepository
	OutboxWorker *worker.OutboxWorker
}

func NewUserService(params UserServiceParams) UserService {
	return &userService{
		userRepo:     params.UserRepo,
		tenantRepo:   params.TenantRepo,
		outboxRepo:   params.OutboxRepo,
		inboxRepo:    params.InboxRepo,
		outboxWorker: params.OutboxWorker,
	}
}

func (s *userService) RegisterUser(ctx context.Context, input RegisterUserInput) (*RegisterUserOutput, error) {
	// 1. Business Logic: Sanitize slug & generate meaningful domain IDs
	cleanSlug := utils.SanitizeSlug(input.TenantSlug)
	userID := "usr_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	tenantID := "tenant_" + cleanSlug

	// 2. Persist User Record via UserRepository (uses transaction from ctx if provided by caller)
	userObj := repository.User{
		ID:    userID,
		Email: input.Email,
		Name:  input.Name,
	}
	if err := s.userRepo.CreateUser(ctx, userObj); err != nil {
		return nil, err
	}

	// 3. Persist Tenant Record via TenantRepository
	tenantObj := repository.Tenant{
		ID:      tenantID,
		Name:    input.TenantName,
		Slug:    cleanSlug,
		OwnerID: userID,
	}
	if err := s.tenantRepo.CreateTenant(ctx, tenantObj); err != nil {
		return nil, err
	}

	// 4. Stage Domain Event Payload inside Transactional Outbox
	evt := publisher.UserRegisteredEvent{
		UserID:     userID,
		TenantID:   tenantID,
		Email:      input.Email,
		Name:       input.Name,
		TenantName: input.TenantName,
		TenantSlug: cleanSlug,
	}
	payloadBytes, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal UserRegistered event payload: %w", err)
	}

	outboxID := "outbox_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	outboxMsg := repository.OutboxMessage{
		ID:            outboxID,
		TenantID:      &tenantID,
		AggregateType: "USER",
		AggregateID:   userID,
		EventType:     "user.registered",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepo.CreateOutboxMessage(ctx, outboxMsg); err != nil {
		return nil, fmt.Errorf("failed to stage outbox event: %w", err)
	}

	log.Printf("UserService: Prepared user_id='%s', tenant_id='%s', outbox_id='%s'", userID, tenantID, outboxID)

	// 5. Wake up worker (non-blocking signal)
	s.outboxWorker.Poke()

	return &RegisterUserOutput{
		UserID:   userID,
		TenantID: tenantID,
	}, nil
}

func (s *userService) HandleTenantProvisioned(ctx context.Context, input HandleTenantProvisionedInput) error {
	// 1. Inbox Guard for event deduplication
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

	// 2. Update tenant placement metadata in public.tenants
	if err := s.tenantRepo.UpdateTenantPlacement(ctx, input.TenantID, input.PlacementType, input.SchemaName, input.DbDSN); err != nil {
		return fmt.Errorf("failed to update tenant placement: %w", err)
	}

	log.Printf("UserService: Successfully updated placement metadata for tenant_id='%s' (placement='%s', schema='%s')",
		input.TenantID, input.PlacementType, input.SchemaName)
	return nil
}
