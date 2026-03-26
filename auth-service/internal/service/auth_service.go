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

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// CredentialRepository is the consumer-side interface expected by AuthService.
type CredentialRepository interface {
	UpsertCredential(ctx context.Context, input repository.UpsertCredentialInput) error
	FindByEmail(ctx context.Context, email string) (*domain.Credential, error)
	FindByUserID(ctx context.Context, userID string) (*domain.Credential, error)
	GetUserMemberships(ctx context.Context, userID string) ([]string, error)
}

// TokenRepository is the consumer-side interface expected by AuthService.
type TokenRepository interface {
	CreateRefreshToken(ctx context.Context, input repository.CreateRefreshTokenInput) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, input repository.RevokeRefreshTokenInput) error
	DeleteRefreshToken(ctx context.Context, input repository.DeleteRefreshTokenInput) error
}

// SetupTokenRepository is the consumer-side interface expected by AuthService.
type SetupTokenRepository interface {
	CreateSetupToken(ctx context.Context, input repository.CreateSetupTokenInput) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*domain.PasswordSetupToken, error)
	MarkTokenUsed(ctx context.Context, tokenHash string) error
}

// UserPermissionProvider is the consumer-side interface for loading permission claims into JWTs.
type UserPermissionProvider interface {
	FindUserPermissions(ctx context.Context, userID, tenantID string) ([]string, int64, error)
}

// RoleSeeder is the consumer-side interface for seeding default roles and admin role assignments for new tenants.
type RoleSeeder interface {
	SeedDefaultRolesForTenant(ctx context.Context, tenantID string, adminUserID string) error
}

// TenantProfileProvider is the consumer-side interface for enriching workspace
// listings with tenant metadata (name/slug/plan) at login time. Implementations
// must be best-effort: failures are tolerated so login never depends on it.
type TenantProfileProvider interface {
	GetTenantProfile(ctx context.Context, tenantID string) (*TenantProfile, error)
}

// TenantProfile carries control-plane metadata for a single workspace.
type TenantProfile struct {
	TenantID string
	Name     string
	Slug     string
	Plan     string
}

type SetupPasswordInput struct {
	Token    string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
}

type WorkspaceInfo struct {
	TenantID   string
	TenantName string
	TenantSlug string
	TenantPlan string
}

type LoginOutput struct {
	Status        string // domain.LoginStatusSuccess or domain.LoginStatusSelectWorkspace
	TokenPair     *TokenPair
	ExchangeToken string
	Workspaces    []WorkspaceInfo
}

type SelectWorkspaceInput struct {
	ExchangeToken string
	TenantID      string
}

type RefreshTokenInput struct {
	RefreshToken string
}

type LogoutInput struct {
	RefreshToken string
}

type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

type AuthService struct {
	credentialRepository CredentialRepository
	tokenRepository      TokenRepository
	setupTokenRepository SetupTokenRepository
	jwtManager           *crypto.JWTManager
	permProvider         UserPermissionProvider
	tenantProfile        TenantProfileProvider
	roleSeeder           RoleSeeder
}

func NewAuthService(
	credentialRepository CredentialRepository,
	tokenRepository TokenRepository,
	setupTokenRepository SetupTokenRepository,
	jwtManager *crypto.JWTManager,
	permProvider UserPermissionProvider,
	tenantProfile TenantProfileProvider,
	roleSeeder ...RoleSeeder,
) *AuthService {
	var seeder RoleSeeder
	if len(roleSeeder) > 0 {
		seeder = roleSeeder[0]
	}
	return &AuthService{
		credentialRepository: credentialRepository,
		tokenRepository:      tokenRepository,
		setupTokenRepository: setupTokenRepository,
		jwtManager:           jwtManager,
		permProvider:         permProvider,
		tenantProfile:        tenantProfile,
		roleSeeder:           seeder,
	}
}

func (s *AuthService) Login(ctx context.Context, input LoginInput) (*LoginOutput, error) {
	if input.Email == "" {
		return nil, domain.ErrEmailRequired
	}
	if input.Password == "" {
		return nil, domain.ErrPasswordRequired
	}

	cred, err := s.credentialRepository.FindByEmail(ctx, input.Email)
	if err != nil {
		if errors.Is(err, domain.ErrCredentialNotFound) {
			return nil, domain.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth service: failed to retrieve credential: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(input.Password)); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	memberships, err := s.credentialRepository.GetUserMemberships(ctx, cred.UserID)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to fetch user memberships: %w", err)
	}

	if len(memberships) == 0 {
		return nil, domain.ErrNoTenantMembership
	}

	rawExchangeToken, tokenHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to generate exchange token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	if err := s.setupTokenRepository.CreateSetupToken(ctx, repository.CreateSetupTokenInput{
		UserID:    cred.UserID,
		TenantID:  "",
		Email:     cred.Email,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, fmt.Errorf("auth service: failed to persist exchange token: %w", err)
	}

	workspaces := make([]WorkspaceInfo, 0, len(memberships))
	for _, m := range memberships {
		workspaces = append(workspaces, s.enrichWorkspace(ctx, m))
	}

	return &LoginOutput{
		Status:        domain.LoginStatusSelectWorkspace,
		ExchangeToken: rawExchangeToken,
		Workspaces:    workspaces,
	}, nil
}

func (s *AuthService) enrichWorkspace(ctx context.Context, tenantID string) WorkspaceInfo {
	wi := WorkspaceInfo{TenantID: tenantID}
	if s.tenantProfile == nil {
		return wi
	}

	profile, err := s.tenantProfile.GetTenantProfile(ctx, tenantID)
	if err != nil {
		log.Printf("AuthService: Warning — failed to enrich workspace '%s': %v", tenantID, err)
		return wi
	}
	if profile != nil {
		wi.TenantName = profile.Name
		wi.TenantSlug = profile.Slug
		wi.TenantPlan = profile.Plan
	}
	return wi
}

func (s *AuthService) SelectWorkspace(ctx context.Context, input SelectWorkspaceInput) (*TokenPair, error) {
	if input.ExchangeToken == "" {
		return nil, domain.ErrTokenNotFound
	}
	if input.TenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}

	tokenHash := crypto.HashRefreshToken(input.ExchangeToken)
	st, err := s.setupTokenRepository.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}

	if st.UsedAt != nil {
		return nil, domain.ErrTokenAlreadyUsed
	}
	if time.Now().UTC().After(st.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	memberships, err := s.credentialRepository.GetUserMemberships(ctx, st.UserID)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to fetch user memberships for exchange: %w", err)
	}

	isMember := false
	for _, m := range memberships {
		if m == input.TenantID {
			isMember = true
			break
		}
	}
	if !isMember {
		return nil, domain.ErrTenantMembershipNotFound
	}

	if err := s.setupTokenRepository.MarkTokenUsed(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("auth service: failed to mark exchange token as used: %w", err)
	}

	return s.issuePair(ctx, st.UserID, input.TenantID, st.Email)
}

func (s *AuthService) RefreshToken(ctx context.Context, input RefreshTokenInput) (*TokenPair, error) {
	if input.RefreshToken == "" {
		return nil, domain.ErrTokenNotFound
	}
	tokenHash := crypto.HashRefreshToken(input.RefreshToken)

	rt, err := s.tokenRepository.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}

	if rt.RevokedAt != nil {
		return nil, domain.ErrTokenRevoked
	}
	if time.Now().UTC().After(rt.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	if err := s.tokenRepository.DeleteRefreshToken(ctx, repository.DeleteRefreshTokenInput{TokenHash: tokenHash}); err != nil {
		return nil, fmt.Errorf("auth service: failed to rotate refresh token: %w", err)
	}

	cred, err := s.credentialRepository.FindByUserID(ctx, rt.UserID)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to retrieve credential for user_id='%s': %w", rt.UserID, err)
	}

	// Preserve the workspace that was active when the refresh token was issued
	// so rotation does not silently drop the tenant context.
	return s.issuePair(ctx, rt.UserID, rt.TenantID, cred.Email)
}

func (s *AuthService) Logout(ctx context.Context, input LogoutInput) error {
	if input.RefreshToken == "" {
		return nil
	}
	tokenHash := crypto.HashRefreshToken(input.RefreshToken)
	if err := s.tokenRepository.RevokeRefreshToken(ctx, repository.RevokeRefreshTokenInput{TokenHash: tokenHash}); err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) {
			return nil
		}
		return fmt.Errorf("auth service: failed to revoke refresh token: %w", err)
	}
	return nil
}

func (s *AuthService) SetupPassword(ctx context.Context, input SetupPasswordInput) (*TokenPair, error) {
	if input.Token == "" {
		return nil, domain.ErrTokenNotFound
	}
	if input.Password == "" {
		return nil, domain.ErrPasswordRequired
	}

	tokenHash := crypto.HashRefreshToken(input.Token)
	st, err := s.setupTokenRepository.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}

	if st.UsedAt != nil {
		return nil, domain.ErrTokenAlreadyUsed
	}
	if time.Now().UTC().After(st.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to hash password: %w", err)
	}

	if err := s.credentialRepository.UpsertCredential(ctx, repository.UpsertCredentialInput{
		UserID:       st.UserID,
		TenantID:     st.TenantID,
		Email:        st.Email,
		PasswordHash: string(hash),
	}); err != nil {
		return nil, fmt.Errorf("auth service: failed to store credential: %w", err)
	}

	if err := s.setupTokenRepository.MarkTokenUsed(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("auth service: failed to mark setup token used: %w", err)
	}

	log.Printf("AuthService: Successfully set password via setup token for user_id='%s'", st.UserID)

	return s.issuePair(ctx, st.UserID, st.TenantID, st.Email)
}

func (s *AuthService) issuePair(ctx context.Context, userID, tenantID, email string) (*TokenPair, error) {
	var permissions []string
	var permVersion int64 = 1

	if s.permProvider != nil && userID != "" && tenantID != "" {
		perms, ver, err := s.permProvider.FindUserPermissions(ctx, userID, tenantID)
		if err == nil {
			permissions = perms
			permVersion = ver
		} else {
			log.Printf("AuthService: Warning — failed to fetch user permissions for user_id='%s': %v", userID, err)
		}
	}

	jti := uuid.New().String()
	accessToken, err := s.jwtManager.SignAccessToken(userID, tenantID, email, jti, permissions, permVersion)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to sign access token: %w", err)
	}

	rawRefresh, refreshHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to generate refresh token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(crypto.RefreshTokenTTL)
	if err := s.tokenRepository.CreateRefreshToken(ctx, repository.CreateRefreshTokenInput{
		UserID:    userID,
		TenantID:  tenantID,
		TokenHash: refreshHash,
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, fmt.Errorf("auth service: failed to persist refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int(crypto.AccessTokenTTL.Seconds()),
	}, nil
}
