package service_test

import (
	"context"
	"errors"
	"testing"

	"auth-service/internal/service"
)

type mockMembershipRepository struct {
	err error
}

func (m *mockMembershipRepository) AddMembership(_ context.Context, userID, tenantID string) error {
	if m.err != nil {
		return m.err
	}
	return nil
}

func TestMembershipService_AddMembership(t *testing.T) {
	membershipService := service.NewMembershipService(&mockMembershipRepository{})
	if err := membershipService.AddMembership(context.Background(), "usr_1", "tnt_1"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestMembershipService_AddMembership_RepoError(t *testing.T) {
	repositoryErr := errors.New("db failure")
	membershipService := service.NewMembershipService(&mockMembershipRepository{err: repositoryErr})

	err := membershipService.AddMembership(context.Background(), "usr_1", "tnt_1")
	if err == nil {
		t.Fatal("expected wrapped repo error, got nil")
	}
	if !errors.Is(err, repositoryErr) {
		t.Fatalf("expected wrapped repo error to be findable via errors.Is, got %v", err)
	}
}