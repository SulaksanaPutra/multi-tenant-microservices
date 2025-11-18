package domain

import (
	"testing"
)

func TestNotificationLog_Struct(t *testing.T) {
	log := NotificationLog{
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
