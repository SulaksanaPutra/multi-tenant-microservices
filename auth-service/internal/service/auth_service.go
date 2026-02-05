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

// --- Consumer-side interfaces (declared in consuming package per AGENTS.md) ---

// CredentialRepository is the interface AuthService requires for credential storage.
type CredentialRepository interface {
	UpsertCredential(ctx context.Context, input repository.CreateCredentialInput) error
	FindByEmail(ctx context.Context, email string) (*domain.Credential, error)
	FindByUserID(ctx context.Context, userID string) (*domain.Credential, error)
}

// TokenRepository is the interface AuthService requires for refresh token storage.
type TokenRepository interface {
	CreateRefreshToken(ctx context.Context, input repository.CreateRefreshTokenInput) error
	FindByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error)
	RevokeByTokenHash(ctx context.Context, tokenHash string) error
	DeleteByTokenHash(ctx context.Context, tokenHash string) error
}

// --- Input / Output types ---

// SetCredentialsInput is the input for the temporary credential provisioning endpoint.
//
// TEMPORARY — NON-PRODUCTION SCAFFOLDING (Stage 1 only)
// This type will be superseded by a proper email-invite / token-gated password-reset
// flow once the OAuth 2.0 stage is implemented.
type SetCredentialsInput struct {
	UserID   string
	TenantID string
	Email    string
	Password string
}

// LoginInput holds the credentials submitted by the client.
type LoginInput struct {
	Email    string
	Password string
}

// TokenPair is the output of a successful login or token refresh.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int // seconds until access token expiry
}

// --- AuthService ---

// AuthService implements the core authentication business logic.
// It is transport-agnostic: no Gin, no HTTP — only context + domain types.
type AuthService struct {
	credentialRepository CredentialRepository
	tokenRepository      TokenRepository
	jwtManager           *crypto.JWTManager
}

// NewAuthService constructs an AuthService with all required dependencies.
func NewAuthService(
	credentialRepository CredentialRepository,
	tokenRepository TokenRepository,
	jwtManager *crypto.JWTManager,
) *AuthService {
	return &AuthService{
		credentialRepository: credentialRepository,
		tokenRepository:      tokenRepository,
		jwtManager:           jwtManager,
	}
}

// SetCredentials creates or replaces the bcrypt password hash for a user identity.
//
// TEMPORARY — NON-PRODUCTION SCAFFOLDING (Stage 1 only)
// This is an open endpoint with no identity verification beyond the fields provided.
// It MUST be replaced by an email-invite / password-reset flow before any production
// deployment or before the OAuth 2.0 stage is reached.
func (s *AuthService) SetCredentials(ctx context.Context, input SetCredentialsInput) error {
	if input.Email == "" {
		return domain.ErrEmailRequired
	}
	if input.Password == "" {
		return domain.ErrPasswordRequired
	}
	if input.UserID == "" {
		return domain.ErrUserIDRequired
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	if err := s.credentialRepository.UpsertCredential(ctx, repository.CreateCredentialInput{
		UserID:       input.UserID,
		TenantID:     input.TenantID,
		Email:        input.Email,
		PasswordHash: string(hash),
	}); err != nil {
		return fmt.Errorf("failed to store credential: %w", err)
	}

	log.Printf("AuthService: Credentials set for email='%s' user_id='%s'", input.Email, input.UserID)
	return nil
}

// Login verifies the provided email/password pair and issues a JWT access token
// plus an opaque refresh token on success.
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
			// Mask the difference between "not found" and "wrong password" — prevent user enumeration.
			return nil, domain.ErrInvalidCredentials
		}
		return nil, fmt.Errorf("failed to retrieve credential: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte(input.Password)); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	return s.issuePair(ctx, cred)
}

// RefreshToken validates the opaque refresh token, rotates it (delete-then-create),
// and returns a fresh access token + new refresh token.
func (s *AuthService) RefreshToken(ctx context.Context, rawRefreshToken string) (*TokenPair, error) {
	tokenHash := crypto.HashRefreshToken(rawRefreshToken)

	rt, err := s.tokenRepository.FindByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err // ErrTokenNotFound propagates as-is
	}

	if rt.RevokedAt != nil {
		return nil, domain.ErrTokenRevoked
	}
	if time.Now().UTC().After(rt.ExpiresAt) {
		return nil, domain.ErrTokenExpired
	}

	// Rotation: hard-delete old token before issuing a new one.
	if err := s.tokenRepository.DeleteByTokenHash(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("failed to rotate refresh token: %w", err)
	}

	cred, err := s.credentialRepository.FindByUserID(ctx, rt.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve credential for user_id='%s': %w", rt.UserID, err)
	}

	return s.issuePair(ctx, cred)
}

// Logout revokes the caller's refresh token, preventing future re-issuance.
// Idempotent: calling Logout with an already-revoked or non-existent token is a no-op.
func (s *AuthService) Logout(ctx context.Context, rawRefreshToken string) error {
	tokenHash := crypto.HashRefreshToken(rawRefreshToken)
	if err := s.tokenRepository.RevokeByTokenHash(ctx, tokenHash); err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) {
			return nil // idempotent
		}
		return fmt.Errorf("failed to revoke refresh token: %w", err)
	}
	return nil
}

// issuePair is the shared helper that signs a JWT and persists a refresh token for the given credential.
func (s *AuthService) issuePair(ctx context.Context, cred *domain.Credential) (*TokenPair, error) {
	jti := uuid.New().String()
	accessToken, err := s.jwtManager.SignAccessToken(cred.UserID, cred.TenantID, cred.Email, jti)
	if err != nil {
		return nil, fmt.Errorf("failed to sign access token: %w", err)
	}

	rawRefresh, refreshHash, err := crypto.GenerateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	expiresAt := time.Now().UTC().Add(crypto.RefreshTokenTTL)
	if err := s.tokenRepository.CreateRefreshToken(ctx, repository.CreateRefreshTokenInput{
		UserID:    cred.UserID,
		TokenHash: refreshHash,
		ExpiresAt: expiresAt,
	}); err != nil {
		return nil, fmt.Errorf("failed to persist refresh token: %w", err)
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: rawRefresh,
		ExpiresIn:    int(crypto.AccessTokenTTL.Seconds()),
	}, nil
}
