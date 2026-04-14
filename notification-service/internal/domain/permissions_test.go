package domain

import "testing"

func TestNotificationServicePermissions(t *testing.T) {
	seen := make(map[string]struct{}, len(NotificationServicePermissions))
	for _, p := range NotificationServicePermissions {
		if p.Name == "" {
			t.Fatalf("permission with empty name: %+v", p)
		}
		if p.Description == "" {
			t.Errorf("permission %q has empty description", p.Name)
		}
		if _, dup := seen[p.Name]; dup {
			t.Errorf("duplicate permission name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
	}
	if len(NotificationServicePermissions) == 0 {
		t.Fatal("expected at least one notification-service permission")
	}
}