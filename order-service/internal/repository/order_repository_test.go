package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/testutil"
	"order-service/internal/txcontext"

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
	repo := NewOrderRepository(cfg)
	if repo == nil {
		t.Fatal("expected NewOrderRepository to return a non-nil struct pointer")
	}
}

func TestOrderRepository_ListOrders_NilDB(t *testing.T) {
	repo := NewOrderRepository(tenantdb.Config{DB: nil})
	_, err := repo.ListOrders(context.Background())
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
	repo := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := repo.ListOrders(ctx)
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
	repo := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := repo.ListOrders(ctx)
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
	repo := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	_, err := repo.ListOrders(ctx)
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
	repo := NewOrderRepository(tenantdb.Config{DB: nil})
	input := CreateOrderInput{
		ID:         "ord-101",
		TenantID:   "tenant-1",
		CustomerID: "cust-1",
		Status:     "PENDING",
		Amount:     99.99,
	}
	err := repo.CreateOrder(context.Background(), input)
	if err == nil {
		t.Fatal("expected error when DB handle is nil, got nil")
	}
	if !strings.Contains(err.Error(), "database handle is nil") {
		t.Errorf("expected 'database handle is nil' error, got: %v", err)
	}
}

func TestOrderRepository_CreateOrder_Success(t *testing.T) {
	dummyDB := getDummyDB()
	var capturedQuery string
	var capturedArgs []any

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = args
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	cfg := tenantdb.Config{
		TenantID:   "tenant-xyz",
		DB:         dummyDB,
		SchemaName: "tenant_xyz",
	}
	repo := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOrderInput{
		ID:         "ord-999",
		TenantID:   "tenant-xyz",
		CustomerID: "cust-888",
		Status:     "PENDING",
		Amount:     150.75,
	}

	err := repo.CreateOrder(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, `INSERT INTO "tenant_xyz".orders`) {
		t.Errorf("expected query to contain 'INSERT INTO \"tenant_xyz\".orders', got: %s", capturedQuery)
	}

	if len(capturedArgs) != 5 {
		t.Fatalf("expected 5 query arguments, got %d", len(capturedArgs))
	}

	if capturedArgs[0] != input.ID || capturedArgs[1] != input.TenantID || capturedArgs[2] != input.CustomerID || capturedArgs[3] != input.Status || capturedArgs[4] != input.Amount {
		t.Errorf("unexpected query arguments: got %v, expected [%s, %s, %s, %s, %f]",
			capturedArgs, input.ID, input.TenantID, input.CustomerID, input.Status, input.Amount)
	}
}

func TestOrderRepository_CreateOrder_DefaultPublicSchema(t *testing.T) {
	dummyDB := getDummyDB()
	var capturedQuery string

	mockExec := &testutil.MockDBExecutor{
		ExecContextFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			return testutil.MockResult{RowsAffectedVal: 1}, nil
		},
	}

	cfg := tenantdb.Config{
		DB:         dummyDB,
		SchemaName: "",
	}
	repo := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOrderInput{
		ID:         "ord-def",
		TenantID:   "tenant-def",
		CustomerID: "cust-def",
		Status:     "CREATED",
		Amount:     50.00,
	}

	err := repo.CreateOrder(ctx, input)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if !strings.Contains(capturedQuery, `INSERT INTO "public".orders`) {
		t.Errorf("expected query to default to \"public\".orders, got: %s", capturedQuery)
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
	repo := NewOrderRepository(cfg)
	ctx := txcontext.WithExecutor(context.Background(), mockExec)

	input := CreateOrderInput{ID: "ord-err"}
	err := repo.CreateOrder(ctx, input)
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
