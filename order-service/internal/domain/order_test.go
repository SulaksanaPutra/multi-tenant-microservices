package domain_test

import (
	"strings"
	"testing"

	"order-service/internal/domain"
)

func TestOrder_Struct(t *testing.T) {
	order := domain.Order{
		ID:         "ord_123",
		TenantID:   "tnt_1",
		CustomerID: "cust_456",
		Status:     "COMPLETED",
		Amount:     99.95,
	}

	if order.ID != "ord_123" || order.Amount != 99.95 || order.Status != "COMPLETED" {
		t.Errorf("unexpected Order struct values: %+v", order)
	}
}

func TestGenerateOrderID(t *testing.T) {
	id := domain.GenerateOrderID()
	if !strings.HasPrefix(id, domain.PrefixOrder) {
		t.Errorf("expected ID prefix '%s', got '%s'", domain.PrefixOrder, id)
	}
	if len(id) != len(domain.PrefixOrder)+16 {
		t.Errorf("expected ID length %d, got %d ('%s')", len(domain.PrefixOrder)+16, len(id), id)
	}
}

