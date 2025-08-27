package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"user-service/internal/infrastructure/postgres"
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

type UserService interface {
	RegisterUser(ctx context.Context, input RegisterUserInput) (*RegisterUserOutput, error)
}

type userService struct {
	dbClient     *postgres.Client
	userRepo     repository.UserRepository
	tenantRepo   repository.TenantRepository
	outboxRepo   repository.OutboxRepository
	outboxWorker *worker.OutboxWorker
}

func NewUserService(
	dbClient *postgres.Client,
	userRepo repository.UserRepository,
	tenantRepo repository.TenantRepository,
	outboxRepo repository.OutboxRepository,
	outboxWorker *worker.OutboxWorker,
) UserService {
	return &userService{
		dbClient:     dbClient,
		userRepo:     userRepo,
		tenantRepo:   tenantRepo,
		outboxRepo:   outboxRepo,
		outboxWorker: outboxWorker,
	}
}

func (s *userService) RegisterUser(ctx context.Context, input RegisterUserInput) (*RegisterUserOutput, error) {
	// 1. Business Logic: Sanitize slug & generate meaningful domain IDs
	cleanSlug := utils.SanitizeSlug(input.TenantSlug)
	userID := "usr_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	tenantID := "tenant_" + cleanSlug

	// 2. Database Transaction Management
	tx, err := s.dbClient.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start database transaction: %w", err)
	}
	defer tx.Rollback()

	// 3. Persist User Record via UserRepository
	userObj := repository.User{
		ID:    userID,
		Email: input.Email,
		Name:  input.Name,
	}
	if err := s.userRepo.CreateUser(ctx, tx, userObj); err != nil {
		return nil, err
	}

	// 4. Persist Tenant Record via TenantRepository
	tenantObj := repository.Tenant{
		ID:      tenantID,
		Name:    input.TenantName,
		Slug:    cleanSlug,
		OwnerID: userID,
	}
	if err := s.tenantRepo.CreateTenant(ctx, tx, tenantObj); err != nil {
		return nil, err
	}

	// 5. Stage Domain Event Payload inside Transactional Outbox.
	//    FIX (Challenge 1): Instead of calling rabbitmq.Publish() directly,
	//    we write the event into the outbox table INSIDE the same transaction.
	//    If the transaction commits, the event is guaranteed to be delivered eventually.
	//    If the transaction rolls back, neither the user nor the event is created.
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

	outboxID := "outbox_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	outboxMsg := repository.OutboxMessage{
		ID:            outboxID,
		TenantID:      &tenantID,
		AggregateType: "USER",
		AggregateID:   userID,
		EventType:     "user.registered",
		Payload:       payloadBytes,
		Status:        "PENDING",
	}
	if err := s.outboxRepo.CreateOutboxMessage(ctx, tx, outboxMsg); err != nil {
		return nil, fmt.Errorf("failed to stage outbox event in transaction: %w", err)
	}

	// Commit atomically: User + Tenant + Outbox Event all succeed together.
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit user, tenant, and outbox transaction: %w", err)
	}

	log.Printf("UserService: Registered user_id='%s', tenant_id='%s', outbox_id='%s'", userID, tenantID, outboxID)

	// 6. Poke Outbox Worker (non-blocking wake-up signal).
	// FIX (Challenge 2): Instead of waiting up to 5 seconds for the ticker to fire,
	// we send an instant signal to the worker goroutine. If 50 users register in
	// the same millisecond, 50 Poke() signals arrive — but the debounce window
	// collapses them into a single batch query.
	s.outboxWorker.Poke()

	return &RegisterUserOutput{
		UserID:   userID,
		TenantID: tenantID,
	}, nil
}
