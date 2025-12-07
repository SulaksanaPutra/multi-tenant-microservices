package domain_test

import (
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
