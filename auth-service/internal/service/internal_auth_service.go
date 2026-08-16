package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"auth-service/internal/crypto"
	"auth-service/internal/domain"
	"auth-service/internal/repository"
)

type InternalCreateSetupTokenInput struct {
	UserID   string
	TenantID string
	Email    string
}

type InternalAuthService struct {
	setupTokenRepository SetupTokenRepository
	membershipRepository MembershipRepository
	roleSeeder           RoleSeeder
}

func NewInternalAuthService(
	setupTokenRepository SetupTokenRepository,
	membershipRepository MembershipRepository,
	roleSeeder ...RoleSeeder,
) *InternalAuthService {
	var seeder RoleSeeder
	if len(roleSeeder) > 0 {
		seeder = roleSeeder[0]
	}
	return &InternalAuthService{
		setupTokenRepository: setupTokenRepository,
		membershipRepository: membershipRepository,
		roleSeeder:           seeder,
	}
}

func (internalAuthService *InternalAuthService) CreatePasswordSetupToken(ctx context.Context, input InternalCreateSetupTokenInput) (string, error) {
	if input.UserID == "" {
		return "", domain.ErrUserIDRequired
	}
	if input.Email == "" {
		return "", domain.ErrEmailRequired
	}

	if internalAuthService.membershipRepository != nil && input.TenantID != "" {
		_ = internalAuthService.membershipRepository.AddMembership(ctx, input.UserID, input.TenantID)
	}

	rawToken, tokenHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return "", fmt.Errorf("internal auth service: failed to generate setup token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	if err := internalAuthService.setupTokenRepository.CreateSetupToken(ctx, repository.CreateSetupTokenInput{
		UserID:    input.UserID,
		TenantID:  input.TenantID,
		Email:     input.Email,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}); err != nil {
		return "", fmt.Errorf("internal auth service: failed to persist setup token: %w", err)
	}

	log.Printf("InternalAuthService: Created password setup token for user_id='%s' email='%s'", input.UserID, input.Email)

	if internalAuthService.roleSeeder != nil && input.TenantID != "" {
		if err := internalAuthService.roleSeeder.SeedDefaultRolesForTenant(ctx, input.TenantID, input.UserID); err != nil {
			log.Printf("InternalAuthService: Warning — failed to seed default roles for tenant_id='%s': %v", input.TenantID, err)
		}
	}

	return rawToken, nil
}
