package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"auth-service/internal/crypto"
	"auth-service/internal/domain"
	"auth-service/internal/repository"
)

// InvitationRoleRepository is the role-related repository surface required by
// InvitationService to validate a role and record a role assignment.
type InvitationRoleRepository interface {
	FindRoleByID(ctx context.Context, id string) (*domain.Role, error)
	AssignUserRole(ctx context.Context, userID, tenantID, roleID string, assignedBy *string) error
}

type InviteUserInput struct {
	Email     string
	RoleID    string
	TenantID  string
	InvitedBy string
}

type InviteUserOutput struct {
	UserID string
	Email  string
	RoleID string
	Token  string
}

// InvitationService provisions a new tenant member and issues an invitation
// (password-setup token) so the invitee can activate their account.
type InvitationService struct {
	membershipRepository MembershipRepository
	roleRepository       InvitationRoleRepository
	setupTokenRepository SetupTokenRepository
}

func NewInvitationService(
	membershipRepository MembershipRepository,
	roleRepository InvitationRoleRepository,
	setupTokenRepository SetupTokenRepository,
) *InvitationService {
	return &InvitationService{
		membershipRepository: membershipRepository,
		roleRepository:       roleRepository,
		setupTokenRepository: setupTokenRepository,
	}
}

// InviteUser validates the request, provisions the invitee as a member of the
// tenant, assigns the requested role, and returns an invitation (setup) token.
func (s *InvitationService) InviteUser(ctx context.Context, input InviteUserInput) (*InviteUserOutput, error) {
	if input.Email == "" {
		return nil, domain.ErrEmailRequired
	}
	if input.TenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}
	if input.RoleID == "" {
		return nil, domain.ErrRoleIDRequired
	}

	if _, err := s.roleRepository.FindRoleByID(ctx, input.RoleID); err != nil {
		if errors.Is(err, domain.ErrRoleNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("invitation service: failed to validate role: %w", err)
	}

	userID := domain.GenerateUserID()

	if err := s.membershipRepository.AddMembership(ctx, userID, input.TenantID); err != nil {
		return nil, fmt.Errorf("invitation service: failed to add membership: %w", err)
	}

	assignedBy := input.InvitedBy
	if err := s.roleRepository.AssignUserRole(ctx, userID, input.TenantID, input.RoleID, &assignedBy); err != nil {
		return nil, fmt.Errorf("invitation service: failed to assign role: %w", err)
	}

	rawToken, tokenHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("invitation service: failed to generate invitation token: %w", err)
	}

	if err := s.setupTokenRepository.CreateSetupToken(ctx, repository.CreateSetupTokenInput{
		UserID:    userID,
		TenantID:  input.TenantID,
		Email:     input.Email,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}); err != nil {
		return nil, fmt.Errorf("invitation service: failed to persist invitation token: %w", err)
	}

	log.Printf("InvitationService: Invited user_id='%s' email='%s' to tenant='%s' with role='%s' by admin='%s'",
		userID, input.Email, input.TenantID, input.RoleID, input.InvitedBy)

	return &InviteUserOutput{
		UserID: userID,
		Email:  input.Email,
		RoleID: input.RoleID,
		Token:  rawToken,
	}, nil
}
