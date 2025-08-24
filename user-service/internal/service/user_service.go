package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"user-service/internal/infrastructure/postgres"
	"user-service/internal/publisher"
	"user-service/internal/repository"
	"user-service/internal/utils"
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
	dbClient   *postgres.Client
	userRepo   repository.UserRepository
	tenantRepo repository.TenantRepository
	publisher  publisher.UserEventPublisher
}

func NewUserService(
	dbClient *postgres.Client,
	userRepo repository.UserRepository,
	tenantRepo repository.TenantRepository,
	pub publisher.UserEventPublisher,
) UserService {
	return &userService{
		dbClient:   dbClient,
		userRepo:   userRepo,
		tenantRepo: tenantRepo,
		publisher:  pub,
	}
}

func (s *userService) RegisterUser(ctx context.Context, input RegisterUserInput) (*RegisterUserOutput, error) {
	cleanSlug := utils.SanitizeSlug(input.TenantSlug)
	userID := "usr_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:16]
	tenantID := "tenant_" + cleanSlug

	// 1. Persist User Record
	tx, err := s.dbClient.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start database transaction: %w", err)
	}
	defer tx.Rollback()

	userObj := repository.User{
		ID:    userID,
		Email: input.Email,
		Name:  input.Name,
	}
	if err := s.userRepo.CreateUser(ctx, tx, userObj); err != nil {
		return nil, err
	}

	tenantObj := repository.Tenant{
		ID:      tenantID,
		Name:    input.TenantName,
		Slug:    cleanSlug,
		OwnerID: userID,
	}
	if err := s.tenantRepo.CreateTenant(ctx, tx, tenantObj); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// 2. Publish event directly to RabbitMQ AFTER the database commit.
	//    BUG: If this call fails (RabbitMQ is down, process crashes, network blips),
	//    the user exists in PostgreSQL but NO event is ever published. The notification
	//    service never sends a welcome email. The tenant service never provisions a schema.
	//    This is the classic Dual-Write Problem.
	evt := publisher.UserRegisteredEvent{
		UserID:     userID,
		TenantID:   tenantID,
		Email:      input.Email,
		Name:       input.Name,
		TenantName: input.TenantName,
		TenantSlug: cleanSlug,
	}
	if err := s.publisher.PublishUserRegistered(ctx, evt); err != nil {
		// The user is already committed to the DB. We cannot roll back.
		// The event is silently lost — no retry, no recovery.
		log.Printf("UserService ERROR: Failed to publish UserRegistered event for user_id='%s': %v", userID, err)
		return nil, fmt.Errorf("failed to publish user registered event: %w", err)
	}

	log.Printf("UserService: Registered user_id='%s', tenant_id='%s'", userID, tenantID)

	return &RegisterUserOutput{
		UserID:   userID,
		TenantID: tenantID,
	}, nil
}
