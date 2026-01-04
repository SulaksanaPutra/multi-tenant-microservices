package domain_test

import (
	"testing"

	"user-service/internal/domain"
)

func TestInboxMessage_Struct(t *testing.T) {
	msg := domain.InboxMessage{
		EventID:   "evt_100",
		TenantID:  "tnt_1",
		EventType: "workspace.initiated",
		Payload:   []byte(`{"test":true}`),
	}

	if msg.EventID != "evt_100" || msg.TenantID != "tnt_1" || msg.EventType != "workspace.initiated" {
		t.Errorf("unexpected InboxMessage values: %+v", msg)
	}
}
