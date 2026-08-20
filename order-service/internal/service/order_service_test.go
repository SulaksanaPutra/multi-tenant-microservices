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
				Quantity:   2,
				Price:      50.25,
				Currency:   "USD",
			},
			wantErr: domain.ErrTenantIDRequired,
		},
		{
			name: "missing customer_id",
			input: CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "",
				Quantity:   2,
				Price:      50.25,
				Currency:   "USD",
			},
			wantErr: domain.ErrCustomerIDRequired,
		},
		{
			name: "invalid quantity zero or negative",
			input: CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "cust-123",
				Quantity:   0,
				Price:      50.25,
				Currency:   "USD",
			},
			wantErr: domain.ErrInvalidQuantity,
		},
		{
			name: "invalid price zero or negative",
			input: CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "cust-123",
				Quantity:   2,
				Price:      0,
				Currency:   "USD",
			},
			wantErr: domain.ErrInvalidPrice,
		},
		{
			name: "missing currency",
			input: CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "cust-123",
				Quantity:   2,
				Price:      50.25,
				Currency:   "",
			},
			wantErr: domain.ErrCurrencyRequired,
		},
		{
			name: "valid input",
			input: CreateOrderInput{
				TenantID:   "tenant-test",
				CustomerID: "cust-123",
				Quantity:   2,
				Price:      50.25,
				Currency:   "USD",
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
			Quantity:   1,
			Price:      50.0,
			Currency:   "USD",
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
			Quantity:   1,
			Price:      50.0,
			Currency:   "USD",
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
			Quantity:   1,
			Price:      25.0,
			Currency:   "USD",
		}
		order, err := orderService.CreateOrder(context.Background(), input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if order.Status != "PENDING" {
			t.Errorf("expected status 'PENDING', got '%s'", order.Status)
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
		Quantity:   1,
		Price:      10.0,
		Currency:   "USD",
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
		expectedOrders := []OrderOutput{
			{ID: "ord-1", TenantID: "t-1", Status: "pending", Quantity: 1, Price: 100, Amount: 100, Currency: "USD"},
			{ID: "ord-2", TenantID: "t-1", Status: "pending", Quantity: 1, Price: 200, Amount: 200, Currency: "USD"},
		}
		orderRepository := &mockOrderRepository{
			listOrdersFunc: func(ctx context.Context) ([]domain.Order, error) {
				return []domain.Order{
					{ID: "ord-1", TenantID: "t-1", Status: "pending", Quantity: 1, Price: 100, Amount: 100, Currency: "USD"},
					{ID: "ord-2", TenantID: "t-1", Status: "pending", Quantity: 1, Price: 200, Amount: 200, Currency: "USD"},
				}, nil
			},
		}
		orderService := NewOrderService(orderRepository)

		orders, err := orderService.ListOrders(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(orders, expectedOrders) {
			t.Errorf("expected orders %v, got %v", expectedOrders, orders)
		}
	})

	t.Run("returns repository error", func(t *testing.T) {
		expectedErr := errors.New("list error")
		orderRepository := &mockOrderRepository{
			listOrdersFunc: func(ctx context.Context) ([]domain.Order, error) {
				return nil, expectedErr
			},
		}
		orderService := NewOrderService(orderRepository)

		_, err := orderService.ListOrders(context.Background())
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestOrderService_ErrorContractInvariants(t *testing.T) {
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
