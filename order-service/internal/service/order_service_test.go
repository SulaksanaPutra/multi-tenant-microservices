package service_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/registry"
	"order-service/internal/repository"
	"order-service/internal/service"
)

type mockOrderRepo struct {
	createdOrders []repository.Order
	getOrdersFn   func(ctx context.Context, db *sql.DB, schemaName string) ([]repository.Order, error)
	createOrderFn func(ctx context.Context, db *sql.DB, schemaName string, order repository.Order) error
}

func (m *mockOrderRepo) GetOrders(ctx context.Context, db *sql.DB, schemaName string) ([]repository.Order, error) {
	if m.getOrdersFn != nil {
		return m.getOrdersFn(ctx, db, schemaName)
	}
	return nil, nil
}

func (m *mockOrderRepo) CreateOrder(ctx context.Context, db *sql.DB, schemaName string, order repository.Order) error {
	if m.createOrderFn != nil {
		return m.createOrderFn(ctx, db, schemaName, order)
	}
	m.createdOrders = append(m.createdOrders, order)
	return nil
}

func TestCreateOrder_InputValidation(t *testing.T) {
	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()
	resolver := tenantdb.NewResolver(tenantdb.ResolverParams{
		PoolRegistry:    poolRegistry,
		RoutingRegistry: routingRegistry,
	})
	repo := &mockOrderRepo{}
	svc := service.NewOrderService(service.OrderServiceParams{
		DBResolver: resolver,
		OrderRepo:  repo,
	})

	ctx := context.Background()

	tests := []struct {
		name    string
		input   service.CreateOrderInput
		wantErr bool
	}{
		{
			name: "missing tenant_id",
			input: service.CreateOrderInput{
				TenantID:   "",
				CustomerID: "cust-123",
				Amount:     100.50,
			},
			wantErr: true,
		},
		{
			name: "missing customer_id",
			input: service.CreateOrderInput{
				TenantID:   "tenant-1",
				CustomerID: "",
				Amount:     100.50,
			},
			wantErr: true,
		},
		{
			name: "invalid amount zero or negative",
			input: service.CreateOrderInput{
				TenantID:   "tenant-1",
				CustomerID: "cust-123",
				Amount:     0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := svc.CreateOrder(ctx, tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("CreateOrder() error = %v, wantErr %v", err, tt.wantErr)
			}
			if order != nil && tt.wantErr {
				t.Errorf("CreateOrder() expected nil order on error, got %v", order)
			}
		})
	}
}

func TestCreateOrder_TenantDSNError(t *testing.T) {
	// Mock tenant-service returning 404
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()
	resolver := tenantdb.NewResolver(tenantdb.ResolverParams{
		PoolRegistry:     poolRegistry,
		RoutingRegistry:  routingRegistry,
		TenantServiceURL: ts.URL,
	})
	repo := &mockOrderRepo{}
	svc := service.NewOrderService(service.OrderServiceParams{
		DBResolver: resolver,
		OrderRepo:  repo,
	})

	ctx := context.Background()
	_, err := svc.CreateOrder(ctx, service.CreateOrderInput{
		TenantID:   "tenant-unknown",
		CustomerID: "cust-123",
		Amount:     50.00,
	})
	if err == nil {
		t.Fatal("expected error when tenant-service returns 404, got nil")
	}
}
