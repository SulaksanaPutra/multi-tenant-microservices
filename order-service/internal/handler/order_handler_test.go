package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"order-service/internal/handler"
	"order-service/internal/repository"
	"order-service/internal/service"

	"github.com/gin-gonic/gin"
)

type mockOrderService struct {
	createOrderFn func(ctx context.Context, input service.CreateOrderInput) (*repository.Order, error)
	getOrdersFn   func(ctx context.Context, tenantID string) ([]repository.Order, error)
}

func (m *mockOrderService) GetOrders(ctx context.Context, tenantID string) ([]repository.Order, error) {
	if m.getOrdersFn != nil {
		return m.getOrdersFn(ctx, tenantID)
	}
	return []repository.Order{}, nil
}

func (m *mockOrderService) CreateOrder(ctx context.Context, input service.CreateOrderInput) (*repository.Order, error) {
	if m.createOrderFn != nil {
		return m.createOrderFn(ctx, input)
	}
	return &repository.Order{
		ID:         "ord-123",
		TenantID:   input.TenantID,
		CustomerID: input.CustomerID,
		Status:     "pending",
		Amount:     input.Amount,
	}, nil
}

func setupTestRouter(svc service.OrderService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewOrderHandler(svc)
	r.POST("/api/orders", h.CreateOrder)
	r.GET("/api/orders", h.GetOrders)
	return r
}

func TestCreateOrder_MissingTenantHeader(t *testing.T) {
	svc := &mockOrderService{}
	router := setupTestRouter(svc)

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
	svc := &mockOrderService{}
	router := setupTestRouter(svc)

	// Amount is <= 0 which violates binding:"required,gt=0"
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
	svc := &mockOrderService{}
	router := setupTestRouter(svc)

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

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201 Created, got %d. Body: %s", w.Code, w.Body.String())
	}
}
