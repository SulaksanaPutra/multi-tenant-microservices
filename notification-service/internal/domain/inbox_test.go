package domain_test

import (
	"testing"

	"notification-service/internal/domain"
)

func TestInboxMessage_Struct(t *testing.T) {
	msg := domain.InboxMessage{
		EventID:   "evt_100",
		TenantID:  "tnt_1",
		EventType: "user.created",
		Payload:   []byte(`{"user_id":"usr_1"}`),
	}

	if msg.EventID != "evt_100" || string(msg.Payload) != `{"user_id":"usr_1"}` {
		t.Errorf("unexpected InboxMessage struct values: %+v", msg)
	}
}
