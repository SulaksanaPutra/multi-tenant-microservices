package domain_test

import (
	"testing"

	"payment-service/internal/domain"
)

func TestDomainPermissions(t *testing.T) {
	permissions := domain.DomainPermissions()
	if len(permissions) == 0 {
		t.Fatal("expected non-empty domain permissions list")
	}

	seen := make(map[string]bool)
	for _, perm := range permissions {
		if perm == "" {
			t.Errorf("found empty permission string in DomainPermissions()")
		}
		if seen[perm] {
			t.Errorf("found duplicate permission string: %s", perm)
		}
		seen[perm] = true
	}

	expectedPerms := []string{
		domain.PermissionPaymentsRead,
		domain.PermissionPaymentsCreate,
		domain.PermissionPaymentsRefund,
		domain.PermissionPaymentsWebhook,
		domain.PermissionPaymentsManage,
	}

	if len(permissions) != len(expectedPerms) {
		t.Errorf("expected %d permissions, got %d", len(expectedPerms), len(permissions))
	}

	for _, expected := range expectedPerms {
		if !seen[expected] {
			t.Errorf("missing expected permission: %s", expected)
		}
	}
}
