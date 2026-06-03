package service_test

import (
	"context"
	"errors"
	"testing"

	"auth-service/internal/service"
)

type mockMembershipRepo struct {
	err error
}

func (m *mockMembershipRepo) AddMembership(_ context.Context, userID, tenantID string) error {
	if m.err != nil {
		return m.err
	}
	return nil
}

func TestMembershipService_AddMembership(t *testing.T) {
	svc := service.NewMembershipService(&mockMembershipRepo{})
	if err := svc.AddMembership(context.Background(), "usr_1", "tnt_1"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestMembershipService_AddMembership_RepoError(t *testing.T) {
	repoErr := errors.New("db failure")
	svc := service.NewMembershipService(&mockMembershipRepo{err: repoErr})

	err := svc.AddMembership(context.Background(), "usr_1", "tnt_1")
	if err == nil {
		t.Fatal("expected wrapped repo error, got nil")
	}
	if !errors.Is(err, repoErr) {
		t.Fatalf("expected wrapped repo error to be findable via errors.Is, got %v", err)
	}
}