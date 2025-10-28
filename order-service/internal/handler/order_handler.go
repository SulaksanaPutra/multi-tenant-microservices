package handler

import (
	"context"
	"errors"
	"net/http"

	"order-service/internal/domain"
	"order-service/internal/httputil"
	"order-service/internal/service"

	"github.com/gin-gonic/gin"
)

type CreateOrderRequest struct {
	CustomerID string  `json:"customer_id" binding:"required"`
	Amount     float64 `json:"amount" binding:"required,gt=0"`
	Status     string  `json:"status"`
}

// OrderService is the consumer-side interface expected by OrderHandler.
type OrderService interface {
	ListOrders(ctx context.Context) ([]domain.Order, error)
	CreateOrder(ctx context.Context, input service.CreateOrderInput) (*domain.Order, error)
}

type OrderHandler struct {
	orderService OrderService
}

func NewOrderHandler(svc OrderService) *OrderHandler {
	return &OrderHandler{
		orderService: svc,
	}
}

func (h *OrderHandler) ListOrders(c *gin.Context) {
	orders, err := h.orderService.ListOrders(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "", orders)
}

func (h *OrderHandler) CreateOrder(c *gin.Context) {
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

	order, err := h.orderService.CreateOrder(c.Request.Context(), input)
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

	httputil.WriteSuccess(c, http.StatusCreated, "Order created successfully", order)
}
