package domain_test

import (
	"testing"
	"time"

	"user-service/internal/domain"
)

func TestOutboxMessage_Struct(t *testing.T) {
	tenantID := "tnt_1"
	lastErr := "connection error"
	now := time.Now()

	msg := domain.OutboxMessage{
		ID:            "outbox_1",
		TenantID:      &tenantID,
		AggregateType: "User",
		AggregateID:   "usr_1",
		EventType:     "user.created",
		Payload:       []byte(`{}`),
		Status:        "pending",
		RetryCount:    0,
		LastError:     &lastErr,
		CreatedAt:     now,
	}

	if msg.ID != "outbox_1" || *msg.TenantID != tenantID || *msg.LastError != lastErr {
		t.Errorf("unexpected OutboxMessage struct values: %+v", msg)
	}
}
