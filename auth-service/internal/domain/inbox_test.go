package domain

import "testing"

func TestInboxMessage_Struct(t *testing.T) {
	msg := InboxMessage{
		EventID:   "evt_100",
		TenantID:  "tnt_1",
		EventType: "user.created",
		Payload:   []byte(`{"user_id":"usr_1"}`),
	}

	if msg.EventID != "evt_100" || msg.TenantID != "tnt_1" || msg.EventType != "user.created" {
		t.Errorf("unexpected InboxMessage values: %+v", msg)
	}
	if string(msg.Payload) != `{"user_id":"usr_1"}` {
		t.Errorf("unexpected InboxMessage payload: %s", msg.Payload)
	}
}
