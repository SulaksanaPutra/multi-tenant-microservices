package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/testutil"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"

	_ "github.com/lib/pq"
)

func getDummyDB() *sql.DB {
	db, _ := sql.Open("postgres", "host=localhost port=1 user=dummy dbname=dummy sslmode=disable")
	_ = db.Close()
	return db
}

func TestOrderRepository_Constructor(t *testing.T) {
	cfg := tenantdb.Config{
		TenantID:   "tenant-1",
		DB:         getDummyDB(),
		SchemaName: "tenant_1",
	}
	orderRepository := NewOrderRepository(cfg)
	if orderRepository == nil {
		t.Fatal("expected NewOrderRepository to return a non-nil struct pointer")
	}
}

func TestOrderRepository_ListOrders_NilDB(t *testing.T) {
	orderRepository := NewOrderRepository(tenantdb.Config{DB: nil})
	_, err := orderRepository.ListOrders(context.Background())
	if err == nil {
		t.Fatal("expected error when DB handle is nil, got nil")
	}
	if !strings.Contains(err.Error(), "database handle is nil") {
		t.Errorf("expected 'database handle is nil' error, got: %v", err)
	}
}

func TestOrderRepository_ListOrders_SchemaFormatting(t *testing.T) {
	dummyDB := getDummyDB()
	var capturedQuery string
	queryErr := errors.New("query executed")

	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			capturedQuery = query
			return nil, queryErr
		},
	}

	cfg := tenantdb.Config{
		TenantID:   "tenant-acme",
		DB:         dummyDB,
		SchemaName: "tenant_acme",
	}
	orderRepository := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := orderRepository.ListOrders(ctx)
	if err == nil {
		t.Fatal("expected error from QueryContextFn, got nil")
	}

	if !strings.Contains(capturedQuery, `"tenant_acme".orders`) {
		t.Errorf("expected query to target \"tenant_acme\".orders, got: %s", capturedQuery)
	}
	if !strings.Contains(err.Error(), "failed to query orders from schema 'tenant_acme'") {
		t.Errorf("expected error message to contain schema name context, got: %v", err)
	}
}

func TestOrderRepository_ListOrders_DefaultPublicSchema(t *testing.T) {
	dummyDB := getDummyDB()
	var capturedQuery string
	queryErr := errors.New("query executed")

	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			capturedQuery = query
			return nil, queryErr
		},
	}

	cfg := tenantdb.Config{
		DB:         dummyDB,
		SchemaName: "",
	}
	orderRepository := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := orderRepository.ListOrders(ctx)
	if err == nil {
		t.Fatal("expected error from QueryContextFn, got nil")
	}

	if !strings.Contains(capturedQuery, `"public".orders`) {
		t.Errorf("expected query to default to \"public\".orders, got: %s", capturedQuery)
	}
}

func TestOrderRepository_ListOrders_QueryError(t *testing.T) {
	dummyDB := getDummyDB()
	dbErr := errors.New("connection reset by peer")

	mockExec := &testutil.MockDBExecutor{
		QueryContextFn: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
			return nil, dbErr
		},
	}

	cfg := tenantdb.Config{DB: dummyDB, SchemaName: "tenant_fail"}
	orderRepository := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := orderRepository.ListOrders(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to query orders from schema 'tenant_fail'") {
		t.Errorf("expected error message to contain schema name context, got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}

func TestOrderRepository_CreateOrder_NilDB(t *testing.T) {
	orderRepository := NewOrderRepository(tenantdb.Config{DB: nil})
	input := CreateOrderInput{
		ID:         "ord-101",
		TenantID:   "tenant-1",
		CustomerID: "cust-1",
		Status:     "PENDING",
		Amount:     99.99,
	}
	err := orderRepository.CreateOrder(context.Background(), input)
	if err == nil {
		t.Fatal("expected error when DB handle is nil, got nil")
	}
	if !strings.Contains(err.Error(), "database handle is nil") {
		t.Errorf("expected 'database handle is nil' error, got: %v", err)
	}
}

func TestOrderRepository_CreateOrder_Success(t *testing.T) {
	dummyDB := getDummyDB()
	var capturedQueries []string
	var capturedOrdersArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQueries = append(capturedQueries, query)
			if len(capturedQueries) == 1 {
				capturedOrdersArgs = args
			}
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	cfg := tenantdb.Config{
		TenantID:   "tenant-xyz",
		DB:         dummyDB,
		SchemaName: "tenant_xyz",
	}
	orderRepository := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOrderInput{
		ID:         "ord-999",
		TenantID:   "tenant-xyz",
		CustomerID: "cust-888",
		Status:     "PENDING",
		Amount:     150.75,
	}

	err := orderRepository.CreateOrder(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(capturedQueries) != 2 {
		t.Fatalf("expected 2 exec queries (orders + outbox), got %d: %v", len(capturedQueries), capturedQueries)
	}

	if !strings.Contains(capturedQueries[0], `INSERT INTO "tenant_xyz".orders`) {
		t.Errorf("expected first query to contain 'INSERT INTO \"tenant_xyz\".orders', got: %s", capturedQueries[0])
	}

	if !strings.Contains(capturedQueries[1], `INSERT INTO "tenant_xyz".outbox`) {
		t.Errorf("expected second query to contain 'INSERT INTO \"tenant_xyz\".outbox', got: %s", capturedQueries[1])
	}

	if len(capturedOrdersArgs) != 5 {
		t.Fatalf("expected 5 query arguments for orders insert, got %d", len(capturedOrdersArgs))
	}

	if capturedOrdersArgs[0] != input.ID || capturedOrdersArgs[1] != input.TenantID || capturedOrdersArgs[2] != input.CustomerID || capturedOrdersArgs[3] != input.Status || capturedOrdersArgs[4] != input.Amount {
		t.Errorf("unexpected query arguments: got %v, expected [%s, %s, %s, %s, %f]",
			capturedOrdersArgs, input.ID, input.TenantID, input.CustomerID, input.Status, input.Amount)
	}
}

func TestOrderRepository_CreateOrder_DefaultPublicSchema(t *testing.T) {
	dummyDB := getDummyDB()
	var capturedQueries []string

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQueries = append(capturedQueries, query)
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	cfg := tenantdb.Config{
		DB:         dummyDB,
		SchemaName: "",
	}
	orderRepository := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOrderInput{
		ID:         "ord-def",
		TenantID:   "tenant-def",
		CustomerID: "cust-def",
		Status:     "CREATED",
		Amount:     50.00,
	}

	err := orderRepository.CreateOrder(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(capturedQueries) != 2 {
		t.Fatalf("expected 2 exec queries (orders + outbox), got %d: %v", len(capturedQueries), capturedQueries)
	}

	if !strings.Contains(capturedQueries[0], `INSERT INTO "public".orders`) {
		t.Errorf("expected first query to default to \"public\".orders, got: %s", capturedQueries[0])
	}

	if !strings.Contains(capturedQueries[1], `INSERT INTO "public".outbox`) {
		t.Errorf("expected second query to default to \"public\".outbox, got: %s", capturedQueries[1])
	}
}

func TestOrderRepository_CreateOrder_ExecError(t *testing.T) {
	dummyDB := getDummyDB()
	dbErr := errors.New("unique constraint violation")

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			return nil, dbErr
		},
	}

	cfg := tenantdb.Config{DB: dummyDB, SchemaName: "tenant_err"}
	orderRepository := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOrderInput{ID: "ord-err"}
	err := orderRepository.CreateOrder(ctx, input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to insert order into schema 'tenant_err'") {
		t.Errorf("expected error message to contain schema name context, got: %v", err)
	}
	if !errors.Is(err, dbErr) {
		t.Errorf("expected underlying error to be dbErr, got %v", err)
	}
}
