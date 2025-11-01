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
	svc, ok := h.getService(c)
	if !ok {
		return
	}

	orders, err := svc.ListOrders(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "", orders)
}

func (h *OrderHandler) CreateOrder(c *gin.Context) {
	svc, ok := h.getService(c)
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

	order, err := svc.CreateOrder(c.Request.Context(), input)
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
