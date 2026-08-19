package domain

import (
	"testing"
	"time"
)

func TestOutboxMessage_Struct(t *testing.T) {
	now := time.Now()
	msg := OutboxMessage{
		EventID:     "evt_1",
		RoutingKey:  "payment.created",
		Payload:     []byte(`{"amount":100}`),
		Status:      "PENDING",
		RetryCount:  0,
		LastError:    "",
		CreatedAt:   now,
		PublishedAt: nil,
	}

	if msg.EventID != "evt_1" || msg.RoutingKey != "payment.created" || msg.Status != "PENDING" {
		t.Errorf("unexpected OutboxMessage values: %+v", msg)
	}
}
