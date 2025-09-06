package service

import (
	"context"
	"testing"
	"time"

	"tenant-service/internal/repository"
)

type mockMemberRepo struct {
	members []repository.Member
}

func (m *mockMemberRepo) GetMembers(ctx context.Context) ([]repository.Member, error) {
	return m.members, nil
}

func TestGetMembers_ExplicitDI(t *testing.T) {
	repo := &mockMemberRepo{
		members: []repository.Member{
			{ID: 1, UserID: "usr_101", Name: "Alice Shared", Email: "alice@shared.com", Role: "owner", JoinedAt: time.Now()},
		},
	}

	svc := NewMemberService(repo)

	res, err := svc.GetMembers(context.Background())
	if err != nil {
		t.Fatalf("unexpected error fetching tenant members: %v", err)
	}

	if len(res.Members) != 1 || res.Members[0].Name != "Alice Shared" {
		t.Errorf("expected member Alice Shared, got: %+v", res.Members)
	}
}
