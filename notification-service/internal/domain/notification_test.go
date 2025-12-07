package domain_test

import (
	"testing"

	"notification-service/internal/domain"
)

func TestNotificationLog_Struct(t *testing.T) {
	log := domain.NotificationLog{
		ID:             1,
		UserID:         "usr_123",
		TenantID:       "tenant_abc",
		RecipientEmail: "owner@company.com",
		Subject:        "Welcome",
		Body:           "Hello World",
		Status:         "sent",
	}

	if log.ID != 1 || log.UserID != "usr_123" || log.TenantID != "tenant_abc" {
		t.Errorf("unexpected NotificationLog struct values: %+v", log)
	}
}

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
