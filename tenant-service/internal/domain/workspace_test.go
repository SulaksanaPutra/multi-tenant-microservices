package domain_test

import (
	"strings"
	"testing"

	"tenant-service/internal/domain"
)

func TestPlanValidation(t *testing.T) {
	tests := []struct {
		plan    domain.Plan
		isValid bool
	}{
		{domain.PlanShared, true},
		{domain.PlanDedicated, true},
		{domain.Plan("invalid"), false},
		{domain.Plan(""), false},
	}

	for _, tt := range tests {
		if got := tt.plan.IsValid(); got != tt.isValid {
			t.Errorf("Plan(%q).IsValid() = %v, want %v", tt.plan, got, tt.isValid)
		}
	}
}

func TestIDGenerators(t *testing.T) {
	tenantID := domain.GenerateTenantID()
	if !strings.HasPrefix(tenantID, domain.PrefixTenant) {
		t.Errorf("GenerateTenantID() = %q, expected prefix %q", tenantID, domain.PrefixTenant)
	}
	// "tnt_" (4 chars) + 16 hex chars = 20 chars total
	if len(tenantID) != 20 {
		t.Errorf("GenerateTenantID() length = %d, want 20", len(tenantID))
	}

	outboxID := domain.GenerateOutboxID()
	if !strings.HasPrefix(outboxID, domain.PrefixOutbox) {
		t.Errorf("GenerateOutboxID() = %q, expected prefix %q", outboxID, domain.PrefixOutbox)
	}
	// "outbox_" (7 chars) + 32 hex chars = 39 chars total
	if len(outboxID) != 39 {
		t.Errorf("GenerateOutboxID() length = %d, want 39", len(outboxID))
	}
}
