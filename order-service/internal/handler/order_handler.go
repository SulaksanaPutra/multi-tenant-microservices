package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SulaksanaPutra/go-microservice-commons/middleware"

	"order-service/internal/domain"
	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/service"

	"github.com/gin-gonic/gin"
)

type CreateOrderRequest struct {
	CustomerID string  `json:"customer_id" binding:"required,max=255"`
	Amount     float64 `json:"amount"      binding:"required,gt=0,lte=1000000"`
	Status     string  `json:"status"      binding:"omitempty,oneof=pending completed cancelled"`
}

type OrderResponse struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	CustomerID string    `json:"customer_id"`
	Status     string    `json:"status"`
	Amount     string    `json:"amount"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func toOrderResponse(o service.OrderOutput) OrderResponse {
	return OrderResponse{
		ID:         o.ID,
		TenantID:   o.TenantID,
		CustomerID: o.CustomerID,
		Status:     o.Status,
		Amount:     strconv.FormatFloat(o.Amount, 'f', 2, 64),
		CreatedAt:  o.CreatedAt,
		UpdatedAt:  o.UpdatedAt,
	}
}

// OrderService is the consumer-side interface expected by OrderHandler.
type OrderService interface {
	ListOrders(ctx context.Context) ([]service.OrderOutput, error)
	CreateOrder(ctx context.Context, input service.CreateOrderInput) (*service.OrderOutput, error)
}

// OrderServiceFactory constructs an OrderService for a given tenant configuration.
// It is supplied by the composition root so Layer 1 never constructs repositories.
type OrderServiceFactory func(cfg tenantdb.Config) OrderService

type OrderHandler struct {
	factory OrderServiceFactory
}

func NewOrderHandler(factory OrderServiceFactory) *OrderHandler {
	if factory == nil {
		panic("order handler: OrderServiceFactory is required — wire it in the composition root")
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

	httputil.WriteSuccess(c, http.StatusOK, "Orders retrieved successfully", resp)
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

	tenantID := c.GetString(middleware.ContextKeyTenantID)

	input := service.CreateOrderInput{
		TenantID:   tenantID,
		CustomerID: req.CustomerID,
		Amount:     req.Amount,
		Status:     req.Status,
	}

	order, err := orderService.CreateOrder(c.Request.Context(), input)
	if err != nil {
		if errors.Is(err, domain.ErrTenantIDRequired) ||
			errors.Is(err, domain.ErrCustomerIDRequired) ||
			errors.Is(err, domain.ErrInvalidAmount) {
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
