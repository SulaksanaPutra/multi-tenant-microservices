package domain_test

import (
	"testing"
	"time"

	"notification-service/internal/domain"
	"notification-service/internal/testutil"
)

func TestUserCreatedEvent_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	userEvt := domain.UserCreatedEvent{
		EventID:   "evt_1",
		UserID:    "usr_1",
		TenantID:  "tnt_1",
		Email:     "user@test.com",
		Name:      "User Test",
		CreatedAt: now,
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, userEvt)
	if unmarshaled.UserID != userEvt.UserID || !unmarshaled.CreatedAt.Equal(userEvt.CreatedAt) {
		t.Errorf("UserCreatedEvent mismatch: %+v vs %+v", unmarshaled, userEvt)
	}
}

func TestWorkspaceReadyEvent_JSON(t *testing.T) {
	readyEvt := domain.WorkspaceReadyEvent{
		EventID:    "evt_2",
		TenantID:   "tnt_1",
		OwnerEmail: "owner@test.com",
		TenantName: "Acme Corp",
		TenantSlug: "acme-corp",
		OwnerName:  "Bob Jones",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, readyEvt)
	if unmarshaled != readyEvt {
		t.Errorf("WorkspaceReadyEvent mismatch: %+v vs %+v", unmarshaled, readyEvt)
	}
}
