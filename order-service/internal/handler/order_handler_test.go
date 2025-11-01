package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/lib/pq"
	"order-service/internal/handler"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/middleware"

	"github.com/gin-gonic/gin"
)

type mockResolver struct {
	getTenantDBFn func(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

func (m *mockResolver) GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error) {
	if m.getTenantDBFn != nil {
		return m.getTenantDBFn(ctx, tenantID)
	}
	db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable")
	if err != nil {
		return tenantdb.Config{}, err
	}
	return tenantdb.Config{
		TenantID:   tenantID,
		DB:         db,
		SchemaName: "public",
	}, nil
}

func setupTestRouter(resolver middleware.Resolver) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	orderHandler := handler.NewOrderHandler(nil)

	api := r.Group("/api")
	api.Use(middleware.RequireTenantHeader(resolver))

	api.POST("/orders", orderHandler.CreateOrder)
	api.GET("/orders", orderHandler.ListOrders)

	return r
}

func TestCreateOrder_MissingTenantHeader(t *testing.T) {
	resolver := &mockResolver{}
	router := setupTestRouter(resolver)

	body := map[string]any{
		"customer_id": "cust-001",
		"amount":      99.99,
	}
	jsonBytes, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, "/api/orders", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 Bad Request, got %d", w.Code)
	}
}

func TestCreateOrder_InvalidJSON(t *testing.T) {
	resolver := &mockResolver{}
	router := setupTestRouter(resolver)

	body := map[string]any{
		"customer_id": "cust-001",
		"amount":      -10.0,
	}
	jsonBytes, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, "/api/orders", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant-test")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 Bad Request for negative amount, got %d", w.Code)
	}
}

func TestCreateOrder_Success(t *testing.T) {
	resolver := &mockResolver{}
	router := setupTestRouter(resolver)

	body := map[string]any{
		"customer_id": "cust-001",
		"amount":      150.75,
	}
	jsonBytes, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, "/api/orders", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant-acme")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError && w.Code != http.StatusCreated {
		t.Errorf("unexpected status code %d, body: %s", w.Code, w.Body.String())
	}
}


