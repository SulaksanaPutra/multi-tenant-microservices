package service_test

import (
	"context"
	"errors"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/service"
)

type mockInvitationMembershipRepo struct {
	memberships map[string]bool
	err         error
}

func (m *mockInvitationMembershipRepo) AddMembership(_ context.Context, userID, tenantID string) error {
	if m.err != nil {
		return m.err
	}
	m.memberships[userID+"|"+tenantID] = true
	return nil
}

type mockInvitationRoleRepo struct {
	roles       map[string]*domain.Role
	assignments []string
	findErr     error
	assignErr   error
}

func (m *mockInvitationRoleRepo) FindRoleByID(_ context.Context, id string) (*domain.Role, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	if r, ok := m.roles[id]; ok {
		return r, nil
	}
	return nil, domain.ErrRoleNotFound
}

func (m *mockInvitationRoleRepo) AssignUserRole(_ context.Context, userID, tenantID, roleID string, assignedBy *string) error {
	if m.assignErr != nil {
		return m.assignErr
	}
	m.assignments = append(m.assignments, userID+"|"+tenantID+"|"+roleID)
	return nil
}

func newInvitationRoleRepo() *mockInvitationRoleRepo {
	return &mockInvitationRoleRepo{
		roles: map[string]*domain.Role{
			"role_001": {ID: "role_001", Name: "admin", IsSystem: false},
		},
	}
}

func TestInvitationService_InviteUser(t *testing.T) {
	newService := func(roleRepo *mockInvitationRoleRepo) (*service.InvitationService, *mockInvitationMembershipRepo, *mockInvitationRoleRepo, *mockInternalSetupTokenRepo) {
		membershipRepo := &mockInvitationMembershipRepo{memberships: make(map[string]bool)}
		tokenRepo := newMockSetupTokenRepo()
		svc := service.NewInvitationService(membershipRepo, roleRepo, tokenRepo)
		return svc, membershipRepo, roleRepo, tokenRepo
	}

	t.Run("missing email -> ErrEmailRequired", func(t *testing.T) {
		svc, _, _, _ := newService(newInvitationRoleRepo())
		_, err := svc.InviteUser(context.Background(), service.InviteUserInput{
			Email: "", RoleID: "role_001", TenantID: "tnt_001", InvitedBy: "admin_001",
		})
		if !errors.Is(err, domain.ErrEmailRequired) {
			t.Fatalf("expected ErrEmailRequired, got %v", err)
		}
	})

	t.Run("missing tenant -> ErrTenantIDRequired", func(t *testing.T) {
		svc, _, _, _ := newService(newInvitationRoleRepo())
		_, err := svc.InviteUser(context.Background(), service.InviteUserInput{
			Email: "a@b.com", RoleID: "role_001", TenantID: "", InvitedBy: "admin_001",
		})
		if !errors.Is(err, domain.ErrTenantIDRequired) {
			t.Fatalf("expected ErrTenantIDRequired, got %v", err)
		}
	})

	t.Run("missing role -> ErrRoleIDRequired", func(t *testing.T) {
		svc, _, _, _ := newService(newInvitationRoleRepo())
		_, err := svc.InviteUser(context.Background(), service.InviteUserInput{
			Email: "a@b.com", RoleID: "", TenantID: "tnt_001", InvitedBy: "admin_001",
		})
		if !errors.Is(err, domain.ErrRoleIDRequired) {
			t.Fatalf("expected ErrRoleIDRequired, got %v", err)
		}
	})

	t.Run("role not found -> ErrRoleNotFound", func(t *testing.T) {
		svc, _, _, _ := newService(newInvitationRoleRepo())
		_, err := svc.InviteUser(context.Background(), service.InviteUserInput{
			Email: "a@b.com", RoleID: "missing", TenantID: "tnt_001", InvitedBy: "admin_001",
		})
		if !errors.Is(err, domain.ErrRoleNotFound) {
			t.Fatalf("expected ErrRoleNotFound, got %v", err)
		}
	})

	t.Run("membership repo error -> returns error", func(t *testing.T) {
		svc, membershipRepo, _, _ := newService(newInvitationRoleRepo())
		membershipRepo.err = errors.New("db insert failed")
		_, err := svc.InviteUser(context.Background(), service.InviteUserInput{
			Email: "a@b.com", RoleID: "role_001", TenantID: "tnt_001", InvitedBy: "admin_001",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("assign role error -> returns error", func(t *testing.T) {
		roleRepo := newInvitationRoleRepo()
		roleRepo.assignErr = errors.New("assign failed")
		svc, _, _, _ := newService(roleRepo)
		_, err := svc.InviteUser(context.Background(), service.InviteUserInput{
			Email: "a@b.com", RoleID: "role_001", TenantID: "tnt_001", InvitedBy: "admin_001",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("success -> membership, role assignment, and setup token persisted", func(t *testing.T) {
		svc, membershipRepo, roleRepo, tokenRepo := newService(newInvitationRoleRepo())

		out, err := svc.InviteUser(context.Background(), service.InviteUserInput{
			Email: "invitee@example.com", RoleID: "role_001", TenantID: "tnt_001", InvitedBy: "admin_001",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if out.UserID == "" {
			t.Fatal("expected generated user id")
		}
		if out.Email != "invitee@example.com" {
			t.Errorf("expected email 'invitee@example.com', got '%s'", out.Email)
		}
		if out.RoleID != "role_001" {
			t.Errorf("expected role 'role_001', got '%s'", out.RoleID)
		}
		if out.Token == "" {
			t.Fatal("expected non-empty invitation token")
		}

		if !membershipRepo.memberships[out.UserID+"|tnt_001"] {
			t.Errorf("expected membership for user '%s' in tenant 'tnt_001'", out.UserID)
		}

		expectedAssignment := out.UserID + "|tnt_001|role_001"
		if len(roleRepo.assignments) != 1 || roleRepo.assignments[0] != expectedAssignment {
			t.Errorf("expected role assignment '%s', got %v", expectedAssignment, roleRepo.assignments)
		}

		if len(tokenRepo.tokens) != 1 {
			t.Errorf("expected 1 setup token in repo, got %d", len(tokenRepo.tokens))
		}
	})
}
