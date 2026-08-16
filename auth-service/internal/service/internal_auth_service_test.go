package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"auth-service/internal/domain"
	"auth-service/internal/repository"
	"auth-service/internal/service"
)

type mockInternalSetupTokenRepository struct {
	tokens map[string]*domain.PasswordSetupToken
	err    error
}

func newMockSetupTokenRepository() *mockInternalSetupTokenRepository {
	return &mockInternalSetupTokenRepository{
		tokens: make(map[string]*domain.PasswordSetupToken),
	}
}

func (m *mockInternalSetupTokenRepository) CreateSetupToken(_ context.Context, input repository.CreateSetupTokenInput) error {
	if m.err != nil {
		return m.err
	}
	m.tokens[input.TokenHash] = &domain.PasswordSetupToken{
		ID:        "setup_1",
		UserID:    input.UserID,
		TenantID:  input.TenantID,
		Email:     input.Email,
		TokenHash: input.TokenHash,
		ExpiresAt: input.ExpiresAt,
	}
	return nil
}

func (m *mockInternalSetupTokenRepository) FindByTokenHash(_ context.Context, tokenHash string) (*domain.PasswordSetupToken, error) {
	if t, ok := m.tokens[tokenHash]; ok {
		return t, nil
	}
	return nil, errors.New("setup token not found")
}

func (m *mockInternalSetupTokenRepository) MarkTokenUsed(_ context.Context, tokenHash string) error {
	if t, ok := m.tokens[tokenHash]; ok {
		now := time.Now().UTC()
		t.UsedAt = &now
		return nil
	}
	return errors.New("setup token not found")
}

type mockRoleSeeder struct {
	seededTenants map[string]string
	err           error
}

func newMockRoleSeeder() *mockRoleSeeder {
	return &mockRoleSeeder{
		seededTenants: make(map[string]string),
	}
}

func (m *mockRoleSeeder) SeedDefaultRolesForTenant(_ context.Context, tenantID string, adminUserID string) error {
	if m.err != nil {
		return m.err
	}
	m.seededTenants[tenantID] = adminUserID
	return nil
}

func TestInternalAuthService_CreatePasswordSetupToken(t *testing.T) {
	t.Run("missing user id -> returns ErrUserIDRequired", func(t *testing.T) {
		setupTokenRepository := newMockSetupTokenRepository()
		internalAuthService := service.NewInternalAuthService(setupTokenRepository, nil)

		_, err := internalAuthService.CreatePasswordSetupToken(context.Background(), service.InternalCreateSetupTokenInput{
			UserID:   "",
			TenantID: "tnt_001",
			Email:    "test@example.com",
		})
		if !errors.Is(err, domain.ErrUserIDRequired) {
			t.Fatalf("expected ErrUserIDRequired, got %v", err)
		}
	})

	t.Run("missing email -> returns ErrEmailRequired", func(t *testing.T) {
		setupTokenRepository := newMockSetupTokenRepository()
		internalAuthService := service.NewInternalAuthService(setupTokenRepository, nil)

		_, err := internalAuthService.CreatePasswordSetupToken(context.Background(), service.InternalCreateSetupTokenInput{
			UserID:   "usr_001",
			TenantID: "tnt_001",
			Email:    "",
		})
		if !errors.Is(err, domain.ErrEmailRequired) {
			t.Fatalf("expected ErrEmailRequired, got %v", err)
		}
	})

	t.Run("repository error -> returns error", func(t *testing.T) {
		setupTokenRepository := newMockSetupTokenRepository()
		setupTokenRepository.err = errors.New("db insert failed")
		internalAuthService := service.NewInternalAuthService(setupTokenRepository, nil)

		_, err := internalAuthService.CreatePasswordSetupToken(context.Background(), service.InternalCreateSetupTokenInput{
			UserID:   "usr_001",
			TenantID: "tnt_001",
			Email:    "test@example.com",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("success with role seeder -> generates token and seeds roles", func(t *testing.T) {
		setupTokenRepository := newMockSetupTokenRepository()
		roleSeeder := newMockRoleSeeder()
		internalAuthService := service.NewInternalAuthService(setupTokenRepository, nil, roleSeeder)

		token, err := internalAuthService.CreatePasswordSetupToken(context.Background(), service.InternalCreateSetupTokenInput{
			UserID:   "usr_001",
			TenantID: "tnt_001",
			Email:    "test@example.com",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token == "" {
			t.Fatal("expected non-empty token string")
		}

		if len(setupTokenRepository.tokens) != 1 {
			t.Errorf("expected 1 token in repo, got %d", len(setupTokenRepository.tokens))
		}
		if roleSeeder.seededTenants["tnt_001"] != "usr_001" {
			t.Errorf("expected role seeder to seed tenant 'tnt_001' with user 'usr_001', got '%s'", roleSeeder.seededTenants["tnt_001"])
		}
	})

	t.Run("success without tenantID -> generates token without calling seeder", func(t *testing.T) {
		setupTokenRepository := newMockSetupTokenRepository()
		roleSeeder := newMockRoleSeeder()
		internalAuthService := service.NewInternalAuthService(setupTokenRepository, nil, roleSeeder)

		token, err := internalAuthService.CreatePasswordSetupToken(context.Background(), service.InternalCreateSetupTokenInput{
			UserID:   "usr_system",
			TenantID: "",
			Email:    "system@example.com",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token == "" {
			t.Fatal("expected non-empty token string")
		}

		if len(roleSeeder.seededTenants) != 0 {
			t.Errorf("expected no tenants seeded when tenantID is empty, got %d", len(roleSeeder.seededTenants))
		}
	})
}
