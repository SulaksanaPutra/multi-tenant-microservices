package handler

import (
	"context"

	"order-service/internal/service"
)

// mockOrderService provides compile-time interface satisfaction check.
type mockOrderService struct{}

func (m *mockOrderService) ListOrders(ctx context.Context) ([]service.OrderOutput, error) {
	return nil, nil
}

func (m *mockOrderService) CreateOrder(ctx context.Context, input service.CreateOrderInput) (*service.OrderOutput, error) {
	return nil, nil
}

var _ OrderService = (*mockOrderService)(nil)
