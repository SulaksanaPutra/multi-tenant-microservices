package domain_test

import (
	"testing"
	"time"

	"user-service/internal/domain"
	"user-service/internal/testutil"
)

func TestWorkspaceInitiatedEvent_JSON(t *testing.T) {
	initEvt := domain.WorkspaceInitiatedEvent{
		EventID:    "evt_1",
		TenantID:   "tnt_1",
		Plan:       "shared",
		OwnerEmail: "owner@test.com",
		OwnerName:  "Owner Test",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, initEvt)
	if unmarshaled != initEvt {
		t.Errorf("unmarshaled WorkspaceInitiatedEvent %+v does not match expected %+v", unmarshaled, initEvt)
	}
}

func TestUserCreatedEvent_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	userEvt := domain.UserCreatedEvent{
		EventID:   "evt_2",
		UserID:    "usr_1",
		TenantID:  "tnt_1",
		Email:     "user@test.com",
		Name:      "User Test",
		CreatedAt: now,
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, userEvt)
	if unmarshaled.UserID != userEvt.UserID || !unmarshaled.CreatedAt.Equal(userEvt.CreatedAt) {
		t.Errorf("unmarshaled UserCreatedEvent %+v does not match expected %+v", unmarshaled, userEvt)
	}
}
