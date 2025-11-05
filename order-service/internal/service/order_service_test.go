package service_test

import (
	"context"
	"errors"
	"testing"

	"order-service/internal/domain"
	"order-service/internal/service"
)

type mockOrderRepository struct {
	createdOrders []domain.Order
	listOrdersFn  func(ctx context.Context) ([]domain.Order, error)
	createOrderFn func(ctx context.Context, order domain.Order) error
}

func (m *mockOrderRepository) ListOrders(ctx context.Context) ([]domain.Order, error) {
	if m.listOrdersFn != nil {
		return m.listOrdersFn(ctx)
	}
	return nil, nil
}

func (m *mockOrderRepository) CreateOrder(ctx context.Context, order domain.Order) error {
	if m.createOrderFn != nil {
		return m.createOrderFn(ctx, order)
	}
	m.createdOrders = append(m.createdOrders, order)
	return nil
}

func TestCreateOrder_InputValidation(t *testing.T) {
	orderRepository := &mockOrderRepository{}
	orderService := service.NewOrderService(orderRepository)

	ctx := context.Background()

	tests := []struct {
		name    string
		input   service.CreateOrderInput
		wantErr error
	}{
		{
			name: "missing tenant_id",
			input: service.CreateOrderInput{
				TenantID:   "",
				CustomerID: "cust-123",
				Amount:     100.50,
			},
			wantErr: service.ErrTenantIDRequired,
		},
		{
			name: "missing customer_id",
			input: service.CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "",
				Amount:     100.50,
			},
			wantErr: service.ErrCustomerIDRequired,
		},
		{
			name: "invalid amount zero or negative",
			input: service.CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "cust-123",
				Amount:     0,
			},
			wantErr: service.ErrInvalidAmount,
		},
		{
			name: "valid input",
			input: service.CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "cust-123",
				Amount:     100.50,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := orderService.CreateOrder(ctx, tt.input)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("CreateOrder() error = %v, wantErr %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Errorf("CreateOrder() unexpected error = %v", err)
			}
			if order != nil && tt.wantErr != nil {
				t.Errorf("CreateOrder() expected nil order on error, got %v", order)
			}
		})
	}
}

