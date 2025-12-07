package domain_test

import (
	"testing"
	"time"

	"tenant-service/internal/domain"
)

func TestOutboxMessage_Struct(t *testing.T) {
	tenantID := "tnt_1"
	lastErr := "db error"
	now := time.Now()

	msg := domain.OutboxMessage{
		ID:            "outbox_123",
		TenantID:      &tenantID,
		AggregateType: "Workspace",
		AggregateID:   "tnt_1",
		EventType:     "workspace.initiated",
		Payload:       []byte(`{}`),
		Status:        "pending",
		RetryCount:    0,
		LastError:     &lastErr,
		CreatedAt:     now,
	}

	if msg.ID != "outbox_123" || *msg.TenantID != tenantID || *msg.LastError != lastErr {
		t.Errorf("unexpected OutboxMessage struct values: %+v", msg)
	}
}
