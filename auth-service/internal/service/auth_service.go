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

type CredentialRepository interface {
	UpsertCredential(ctx context.Context, input repository.UpsertCredentialInput) error
	FindByEmail(ctx context.Context, email string) (*domain.Credential, error)
	FindByUserID(ctx context.Context, userID string) (*domain.Credential, error)
	ListUserMemberships(ctx context.Context, userID string) ([]string, error)
}

type TokenRepository interface {
	CreateRefreshToken(ctx context.Context, input repository.CreateRefreshTokenInput) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, input repository.RevokeRefreshTokenInput) error
	DeleteRefreshToken(ctx context.Context, input repository.DeleteRefreshTokenInput) error
}

type SetupTokenRepository interface {
	CreateSetupToken(ctx context.Context, input repository.CreateSetupTokenInput) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*domain.PasswordSetupToken, error)
	MarkTokenUsed(ctx context.Context, tokenHash string) error
}

type UserPermissionProvider interface {
	FindUserPermissions(ctx context.Context, userID, tenantID string) ([]string, int64, error)
}

type RoleSeeder interface {
	SeedDefaultRolesForTenant(ctx context.Context, tenantID string, adminUserID string) error
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
	TenantID string
}

type LoginOutput struct {
	Status        string
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
	permissionProvider   UserPermissionProvider
	roleSeeder           RoleSeeder
}

func NewAuthService(
	credentialRepository CredentialRepository,
	tokenRepository TokenRepository,
	setupTokenRepository SetupTokenRepository,
	jwtManager *crypto.JWTManager,
	permissionProvider UserPermissionProvider,
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
		permissionProvider:   permissionProvider,
		roleSeeder:           seeder,
	}
}

func (authService *AuthService) Login(ctx context.Context, input LoginInput) (*LoginOutput, error) {
	if input.Email == "" {
		return nil, domain.ErrEmailRequired
	}
	if input.Password == "" {
		return nil, domain.ErrPasswordRequired
	}

	cred, err := authService.credentialRepository.FindByEmail(ctx, input.Email)
	if err != nil {
		if errors.Is(err, domain.ErrCredentialNotFound) {
			return nil, domain.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("auth service: failed to retrieve credential: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(input.Password)); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	memberships, err := authService.credentialRepository.ListUserMemberships(ctx, cred.UserID)
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
	if err := authService.setupTokenRepository.CreateSetupToken(ctx, repository.CreateSetupTokenInput{
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
		workspaces = append(workspaces, WorkspaceInfo{TenantID: m})
	}

	return &LoginOutput{
		Status:        domain.LoginStatusSelectWorkspace,
		ExchangeToken: rawExchangeToken,
		Workspaces:    workspaces,
	}, nil
}

func (authService *AuthService) SelectWorkspace(ctx context.Context, input SelectWorkspaceInput) (*TokenPair, error) {
	if input.ExchangeToken == "" {
		return nil, domain.ErrTokenNotFound
	}
	if input.TenantID == "" {
		return nil, domain.ErrTenantIDRequired
	}

	tokenHash := crypto.HashRefreshToken(input.ExchangeToken)
	setupToken, err := authService.setupTokenRepository.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}

	if setupToken.UsedAt != nil {
		return nil, domain.ErrTokenAlreadyUsed
	}
	if time.Now().UTC().After(setupToken.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	memberships, err := authService.credentialRepository.ListUserMemberships(ctx, setupToken.UserID)
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

	if err := authService.setupTokenRepository.MarkTokenUsed(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("auth service: failed to mark exchange token as used: %w", err)
	}

	return authService.issuePair(ctx, setupToken.UserID, input.TenantID, setupToken.Email)
}

func (authService *AuthService) RefreshToken(ctx context.Context, input RefreshTokenInput) (*TokenPair, error) {
	if input.RefreshToken == "" {
		return nil, domain.ErrTokenNotFound
	}
	tokenHash := crypto.HashRefreshToken(input.RefreshToken)

	refreshToken, err := authService.tokenRepository.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}

	if refreshToken.RevokedAt != nil {
		return nil, domain.ErrTokenRevoked
	}
	if time.Now().UTC().After(refreshToken.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	if err := authService.tokenRepository.DeleteRefreshToken(ctx, repository.DeleteRefreshTokenInput{TokenHash: tokenHash}); err != nil {
		return nil, fmt.Errorf("auth service: failed to rotate refresh token: %w", err)
	}

	cred, err := authService.credentialRepository.FindByUserID(ctx, refreshToken.UserID)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to retrieve credential for user_id='%s': %w", refreshToken.UserID, err)
	}

	return authService.issuePair(ctx, refreshToken.UserID, refreshToken.TenantID, cred.Email)
}

func (authService *AuthService) Logout(ctx context.Context, input LogoutInput) error {
	if input.RefreshToken == "" {
		return nil
	}
	tokenHash := crypto.HashRefreshToken(input.RefreshToken)
	if err := authService.tokenRepository.RevokeRefreshToken(ctx, repository.RevokeRefreshTokenInput{TokenHash: tokenHash}); err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) {
			return nil
		}
		return fmt.Errorf("auth service: failed to revoke refresh token: %w", err)
	}
	return nil
}

func (authService *AuthService) SetupPassword(ctx context.Context, input SetupPasswordInput) (*TokenPair, error) {
	if input.Token == "" {
		return nil, domain.ErrTokenNotFound
	}
	if input.Password == "" {
		return nil, domain.ErrPasswordRequired
	}

	tokenHash := crypto.HashRefreshToken(input.Token)
	setupToken, err := authService.setupTokenRepository.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}

	if setupToken.UsedAt != nil {
		return nil, domain.ErrTokenAlreadyUsed
	}
	if time.Now().UTC().After(setupToken.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to hash password: %w", err)
	}

	if err := authService.credentialRepository.UpsertCredential(ctx, repository.UpsertCredentialInput{
		UserID:       setupToken.UserID,
		TenantID:     setupToken.TenantID,
		Email:        setupToken.Email,
		PasswordHash: string(hash),
	}); err != nil {
		return nil, fmt.Errorf("auth service: failed to store credential: %w", err)
	}

	if err := authService.setupTokenRepository.MarkTokenUsed(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("auth service: failed to mark setup token used: %w", err)
	}

	log.Printf("AuthService: Successfully set password via setup token for user_id='%s'", setupToken.UserID)

	return authService.issuePair(ctx, setupToken.UserID, setupToken.TenantID, setupToken.Email)
}

func (authService *AuthService) issuePair(ctx context.Context, userID, tenantID, email string) (*TokenPair, error) {
	var permissions []string
	var permVersion int64 = 1

	if authService.permissionProvider != nil && userID != "" && tenantID != "" {
		perms, ver, err := authService.permissionProvider.FindUserPermissions(ctx, userID, tenantID)
		if err == nil {
			permissions = perms
			permVersion = ver
		} else {
			log.Printf("AuthService: Warning — failed to fetch user permissions for user_id='%s': %v", userID, err)
		}
	}

	jti := uuid.New().String()
	accessToken, err := authService.jwtManager.SignAccessToken(userID, tenantID, email, jti, permissions, permVersion)
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to sign access token: %w", err)
	}

	rawRefresh, refreshHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("auth service: failed to generate refresh token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(crypto.RefreshTokenTTL)
	if err := authService.tokenRepository.CreateRefreshToken(ctx, repository.CreateRefreshTokenInput{
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
