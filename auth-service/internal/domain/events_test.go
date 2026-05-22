package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestUserCreatedEvent_JSON(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	evt := UserCreatedEvent{
		EventID:   "evt-user-1",
		UserID:    "usr_100",
		TenantID:  "tenant-99",
		Email:     "john@example.com",
		Name:      "John Doe",
		CreatedAt: now,
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("failed to marshal UserCreatedEvent: %v", err)
	}

	var unmarshaled UserCreatedEvent
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal UserCreatedEvent: %v", err)
	}

	if unmarshaled.EventID != evt.EventID ||
		unmarshaled.UserID != evt.UserID ||
		unmarshaled.TenantID != evt.TenantID ||
		unmarshaled.Email != evt.Email ||
		unmarshaled.Name != evt.Name {
		t.Errorf("unmarshaled UserCreatedEvent mismatch: got %+v, want %+v", unmarshaled, evt)
	}
}

func TestUserCreatedEvent_MatchesUserServicePayload(t *testing.T) {
	// Contract guard: the auth-service consumer must decode the exact payload
	// shape that user-service publishes on the user.created routing key.
	userServicePayload := []byte(`{
		"event_id": "evt-user-1",
		"user_id": "usr_100",
		"tenant_id": "tenant-99",
		"email": "john@example.com",
		"name": "John Doe",
		"created_at": "2026-08-01T12:00:00Z"
	}`)

	var evt UserCreatedEvent
	if err := json.Unmarshal(userServicePayload, &evt); err != nil {
		t.Fatalf("failed to unmarshal user-service payload: %v", err)
	}
	if evt.EventID != "evt-user-1" || evt.UserID != "usr_100" || evt.TenantID != "tenant-99" {
		t.Errorf("unexpected decoded event: %+v", evt)
	}
}

func TestEventTopologyConstants(t *testing.T) {
	if ExchangeCompanyEvents != "company.events" {
		t.Errorf("ExchangeCompanyEvents = %q, want %q", ExchangeCompanyEvents, "company.events")
	}
	if ExchangeCompanyEventsDLX != "company.events.dlx" {
		t.Errorf("ExchangeCompanyEventsDLX = %q, want %q", ExchangeCompanyEventsDLX, "company.events.dlx")
	}
	if RoutingKeyUserCreated != "user.created" {
		t.Errorf("RoutingKeyUserCreated = %q, want %q", RoutingKeyUserCreated, "user.created")
	}
	if QueueAuthUserCreated != "auth_service_user_created_membership" {
		t.Errorf("QueueAuthUserCreated = %q, want %q", QueueAuthUserCreated, "auth_service_user_created_membership")
	}
	if QueueAuthUserCreatedDLQ != "auth_service_user_created_membership_dlq" {
		t.Errorf("QueueAuthUserCreatedDLQ = %q, want %q", QueueAuthUserCreatedDLQ, "auth_service_user_created_membership_dlq")
	}
	if MaxAuthUserCreatedDeliveries < 1 {
		t.Errorf("MaxAuthUserCreatedDeliveries = %d, want >= 1", MaxAuthUserCreatedDeliveries)
	}
}
