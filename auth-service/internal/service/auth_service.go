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

type CreateSetupTokenInput struct {
	UserID   string
	TenantID string
	Email    string
}

type SetupPasswordInput struct {
	Token    string
	Password string
}

type LoginInput struct {
	Email    string
	Password string
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
	roleSeeder           RoleSeeder
}

func NewAuthService(
	credentialRepository CredentialRepository,
	tokenRepository TokenRepository,
	setupTokenRepository SetupTokenRepository,
	jwtManager *crypto.JWTManager,
	permProvider UserPermissionProvider,
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
		roleSeeder:           seeder,
	}
}

func (s *AuthService) Login(ctx context.Context, input LoginInput) (*TokenPair, error) {
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

	return s.issuePair(ctx, cred)
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

	return s.issuePair(ctx, cred)
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

func (s *AuthService) CreatePasswordSetupToken(ctx context.Context, input CreateSetupTokenInput) (string, error) {
	if input.UserID == "" {
		return "", domain.ErrUserIDRequired
	}
	if input.Email == "" {
		return "", domain.ErrEmailRequired
	}

	rawToken, tokenHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return "", fmt.Errorf("auth service: failed to generate setup token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	if err := s.setupTokenRepository.CreateSetupToken(ctx, repository.CreateSetupTokenInput{
		UserID:    input.UserID,
		TenantID:  input.TenantID,
		Email:     input.Email,
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	}); err != nil {
		return "", fmt.Errorf("auth service: failed to persist setup token: %w", err)
	}

	log.Printf("AuthService: Created password setup token for user_id='%s' email='%s'", input.UserID, input.Email)

	if s.roleSeeder != nil && input.TenantID != "" {
		if err := s.roleSeeder.SeedDefaultRolesForTenant(ctx, input.TenantID, input.UserID); err != nil {
			log.Printf("AuthService: Warning — failed to seed default roles for tenant_id='%s': %v", input.TenantID, err)
		}
	}

	return rawToken, nil
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

	cred := &domain.Credential{
		UserID:   st.UserID,
		TenantID: st.TenantID,
		Email:    st.Email,
	}
	return s.issuePair(ctx, cred)
}

func (s *AuthService) issuePair(ctx context.Context, cred *domain.Credential) (*TokenPair, error) {
	var permissions []string
	var permVersion int64 = 1

	if s.permProvider != nil && cred.UserID != "" && cred.TenantID != "" {
		perms, ver, err := s.permProvider.FindUserPermissions(ctx, cred.UserID, cred.TenantID)
		if err == nil {
			permissions = perms
			permVersion = ver
		} else {
			log.Printf("AuthService: Warning — failed to fetch user permissions for user_id='%s': %v", cred.UserID, err)
		}
	}

	jti := uuid.New().String()
	accessToken, err := s.jwtManager.SignAccessToken(cred.UserID, cred.TenantID, cred.Email, jti, permissions, permVersion)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to sign access token: %w", err)
	}

	rawRefresh, refreshHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to generate refresh token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(crypto.RefreshTokenTTL)
	if err := s.tokenRepository.CreateRefreshToken(ctx, repository.CreateRefreshTokenInput{
		UserID:    cred.UserID,
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
