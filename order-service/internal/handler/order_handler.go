package handler

import (
	"context"
	"errors"
	"net/http"

	"order-service/internal/domain"
	"order-service/internal/httputil"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/repository"
	"order-service/internal/service"

	"github.com/gin-gonic/gin"
)

type CreateOrderRequest struct {
	CustomerID string  `json:"customer_id" binding:"required"`
	Amount     float64 `json:"amount" binding:"required,gt=0"`
	Status     string  `json:"status"`
}

type OrderResponse struct {
	ID         string  `json:"id"`
	TenantID   string  `json:"tenant_id"`
	CustomerID string  `json:"customer_id"`
	Status     string  `json:"status"`
	Amount     float64 `json:"amount"`
}

func toOrderResponse(o domain.Order) OrderResponse {
	return OrderResponse{
		ID:         o.ID,
		TenantID:   o.TenantID,
		CustomerID: o.CustomerID,
		Status:     o.Status,
		Amount:     o.Amount,
	}
}

// OrderService is the consumer-side interface expected by OrderHandler.
type OrderService interface {
	ListOrders(ctx context.Context) ([]domain.Order, error)
	CreateOrder(ctx context.Context, input service.CreateOrderInput) (*domain.Order, error)
}

// OrderServiceFactory constructs an OrderService for a given tenant configuration.
type OrderServiceFactory func(cfg tenantdb.Config) OrderService

type OrderHandler struct {
	factory OrderServiceFactory
}

func NewOrderHandler(factory OrderServiceFactory) *OrderHandler {
	if factory == nil {
		factory = func(cfg tenantdb.Config) OrderService {
			repo := repository.NewOrderRepository(cfg)
			return service.NewOrderService(repo)
		}
	}
	return &OrderHandler{
		factory: factory,
	}
}

func (h *OrderHandler) getService(c *gin.Context) (OrderService, bool) {
	cfgVal, ok := c.Get("tenantConfig")
	if !ok {
		httputil.WriteError(c, http.StatusInternalServerError, "tenant database configuration missing from context")
		return nil, false
	}
	tenantCfg, ok := cfgVal.(tenantdb.Config)
	if !ok {
		httputil.WriteError(c, http.StatusInternalServerError, "invalid tenant database configuration type")
		return nil, false
	}
	return h.factory(tenantCfg), true
}

func (h *OrderHandler) ListOrders(c *gin.Context) {
	orderService, ok := h.getService(c)
	if !ok {
		return
	}

	orders, err := orderService.ListOrders(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]OrderResponse, len(orders))
	for i, o := range orders {
		resp[i] = toOrderResponse(o)
	}

	httputil.WriteSuccess(c, http.StatusOK, "", resp)
}

func (h *OrderHandler) CreateOrder(c *gin.Context) {
	orderService, ok := h.getService(c)
	if !ok {
		return
	}

	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, http.StatusBadRequest, err.Error())
		return
	}

	tenantID := c.GetString("tenantID")

	input := service.CreateOrderInput{
		TenantID:   tenantID,
		CustomerID: req.CustomerID,
		Amount:     req.Amount,
		Status:     req.Status,
	}

	order, err := orderService.CreateOrder(c.Request.Context(), input)
	if err != nil {
		if errors.Is(err, service.ErrTenantIDRequired) ||
			errors.Is(err, service.ErrCustomerIDRequired) ||
			errors.Is(err, service.ErrInvalidAmount) {
			httputil.WriteError(c, http.StatusBadRequest, err.Error())
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	var orderResp OrderResponse
	if order != nil {
		orderResp = toOrderResponse(*order)
	}

	httputil.WriteSuccess(c, http.StatusCreated, "Order created successfully", orderResp)
}
