package service

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/repository"
)

type mockOrderRepository struct {
	createdOrders   []repository.CreateOrderInput
	listOrdersFunc  func(ctx context.Context) ([]domain.Order, error)
	createOrderFunc func(ctx context.Context, input repository.CreateOrderInput) error
}

func (m *mockOrderRepository) ListOrders(ctx context.Context) ([]domain.Order, error) {
	if m.listOrdersFunc != nil {
		return m.listOrdersFunc(ctx)
	}
	return nil, nil
}

func (m *mockOrderRepository) CreateOrder(ctx context.Context, input repository.CreateOrderInput) error {
	if m.createOrderFunc != nil {
		return m.createOrderFunc(ctx, input)
	}
	m.createdOrders = append(m.createdOrders, input)
	return nil
}

func TestOrderService_CreateOrder_InputValidation(t *testing.T) {
	orderRepository := &mockOrderRepository{}
	orderService := NewOrderService(orderRepository)

	ctx := context.Background()

	tests := []struct {
		name    string
		input   CreateOrderInput
		wantErr error
	}{
		{
			name: "missing tenant_id",
			input: CreateOrderInput{
				TenantID:   "",
				CustomerID: "cust-123",
				Amount:     100.50,
			},
			wantErr: domain.ErrTenantIDRequired,
		},
		{
			name: "missing customer_id",
			input: CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "",
				Amount:     100.50,
			},
			wantErr: domain.ErrCustomerIDRequired,
		},
		{
			name: "invalid amount zero or negative",
			input: CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "cust-123",
				Amount:     0,
			},
			wantErr: domain.ErrInvalidAmount,
		},
		{
			name: "valid input",
			input: CreateOrderInput{
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

func TestOrderService_CreateOrder_TenantIDResolutionFromContext(t *testing.T) {
	orderRepository := &mockOrderRepository{}
	orderService := NewOrderService(orderRepository)

	t.Run("resolves tenantID from standard context key", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), "tenantID", "tenant-ctx-999")
		input := CreateOrderInput{
			CustomerID: "cust-1",
			Amount:     50.0,
		}
		order, err := orderService.CreateOrder(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if order.TenantID != "tenant-ctx-999" {
			t.Errorf("expected tenantID 'tenant-ctx-999', got '%s'", order.TenantID)
		}
	})

	t.Run("resolves tenantID from tenantdb context", func(t *testing.T) {
		cfg := tenantdb.Config{TenantID: "tenant-cfg-888"}
		ctx := tenantdb.WithConfig(context.Background(), cfg)
		input := CreateOrderInput{
			CustomerID: "cust-1",
			Amount:     50.0,
		}
		order, err := orderService.CreateOrder(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if order.TenantID != "tenant-cfg-888" {
			t.Errorf("expected tenantID 'tenant-cfg-888', got '%s'", order.TenantID)
		}
	})
}

func TestOrderService_CreateOrder_DefaultStatus(t *testing.T) {
	orderRepository := &mockOrderRepository{}
	orderService := NewOrderService(orderRepository)

	t.Run("defaults empty status to pending", func(t *testing.T) {
		input := CreateOrderInput{
			TenantID:   "t-1",
			CustomerID: "c-1",
			Amount:     25.0,
			Status:     "",
		}
		order, err := orderService.CreateOrder(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if order.Status != "pending" {
			t.Errorf("expected status 'pending', got '%s'", order.Status)
		}
	})

	t.Run("preserves explicit status", func(t *testing.T) {
		input := CreateOrderInput{
			TenantID:   "t-1",
			CustomerID: "c-1",
			Amount:     25.0,
			Status:     "completed",
		}
		order, err := orderService.CreateOrder(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if order.Status != "completed" {
			t.Errorf("expected status 'completed', got '%s'", order.Status)
		}
	})
}

func TestOrderService_CreateOrder_RepoError(t *testing.T) {
	expectedErr := errors.New("db error")
	orderRepository := &mockOrderRepository{
		createOrderFunc: func(ctx context.Context, input repository.CreateOrderInput) error {
			return expectedErr
		},
	}
	orderService := NewOrderService(orderRepository)

	input := CreateOrderInput{
		TenantID:   "t-1",
		CustomerID: "c-1",
		Amount:     10.0,
	}

	order, err := orderService.CreateOrder(context.Background(), input)
	if err == nil || !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping '%v', got '%v'", expectedErr, err)
	}
	if order != nil {
		t.Errorf("expected nil order on error, got %v", order)
	}
}

func TestOrderService_ListOrders(t *testing.T) {
	t.Run("returns list of orders", func(t *testing.T) {
		expectedOrders := []domain.Order{
			{ID: "ord-1", TenantID: "t-1", Amount: 100},
			{ID: "ord-2", TenantID: "t-1", Amount: 200},
		}
		repo := &mockOrderRepository{
			listOrdersFunc: func(ctx context.Context) ([]domain.Order, error) {
				return expectedOrders, nil
			},
		}
		svc := NewOrderService(repo)

		orders, err := svc.ListOrders(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(orders, expectedOrders) {
			t.Errorf("expected orders %v, got %v", expectedOrders, orders)
		}
	})

	t.Run("returns repository error", func(t *testing.T) {
		expectedErr := errors.New("list error")
		repo := &mockOrderRepository{
			listOrdersFunc: func(ctx context.Context) ([]domain.Order, error) {
				return nil, expectedErr
			},
		}
		svc := NewOrderService(repo)

		_, err := svc.ListOrders(context.Background())
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestOrderService_ErrorContractInvariants(t *testing.T) {
	// Sentinel errors were centralized into the domain package by the
	// "centralize service errors" refactor; each message still carries the
	// owning service/domain prefix.
	knownPrefixes := []string{
		"domain:",
		"order service:",
	}

	const errorsFile = "../domain/errors.go"

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, errorsFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse %s AST: %v", errorsFile, err)
	}

	var errorVarsFound int
	for _, decl := range node.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if !strings.HasPrefix(name.Name, "Err") {
					t.Errorf("sentinel error variable '%s' in %s must start with prefix 'Err'", name.Name, errorsFile)
				}
				errorVarsFound++
				if i < len(valueSpec.Values) {
					if call, ok := valueSpec.Values[i].(*ast.CallExpr); ok {
						if len(call.Args) > 0 {
							if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								errStr := strings.Trim(lit.Value, `"`)
								matched := false
								for _, prefix := range knownPrefixes {
									if strings.HasPrefix(errStr, prefix) {
										matched = true
										break
									}
								}
								if !matched {
									t.Errorf("sentinel error '%s' message '%s' in %s must start with a known service prefix (%v)", name.Name, errStr, errorsFile, knownPrefixes)
								}
							}
						}
					}
				}
			}
		}
	}
	if errorVarsFound == 0 {
		t.Errorf("expected at least one sentinel error declaration in %s", errorsFile)
	}
}
