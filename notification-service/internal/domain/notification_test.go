package domain_test

import (
	"strings"
	"testing"

	"notification-service/internal/domain"
)

func TestNotificationLog_Struct(t *testing.T) {
	log := domain.NotificationLog{
		ID:          "ntf_abc",
		UserID:      "usr_123",
		TenantID:    "tenant_abc",
		Description: "Welcome",
		Body:        "Hello World",
		Status:      "sent",
	}

	if log.ID != "ntf_abc" || log.UserID != "usr_123" || log.TenantID != "tenant_abc" {
		t.Errorf("unexpected NotificationLog struct values: %+v", log)
	}
}

func TestGenerateNotificationID(t *testing.T) {
	id := domain.GenerateNotificationID()
	if !strings.HasPrefix(id, domain.PrefixNotification) {
		t.Errorf("expected generated id to be prefixed with %q, got %q", domain.PrefixNotification, id)
	}
	if len(id) != len(domain.PrefixNotification)+32 {
		t.Errorf("expected generated id length to be %d, got %d (%q)", len(domain.PrefixNotification)+32, len(id), id)
	}
	if id == domain.GenerateNotificationID() {
		t.Error("expected generated notification ids to be unique")
	}
}
