package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"

	"order-service/internal/domain"
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

type OrderHandler struct {
	factory          OrderServiceFactory
	txManagerFactory TxManagerFactory
}

func NewOrderHandler(factory OrderServiceFactory, opts ...func(*OrderHandler)) *OrderHandler {
	if factory == nil {
		panic("order handler: OrderServiceFactory is required — wire it in the composition root")
	}
	h := &OrderHandler{
		factory: factory,
		txManagerFactory: func(cfg tenantdb.Config) TxManager {
			return txcontext.NewTxManager(cfg.DB)
		},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

func WithTxManagerFactory(txFactory TxManagerFactory) func(*OrderHandler) {
	return func(h *OrderHandler) {
		if txFactory != nil {
			h.txManagerFactory = txFactory
		}
	}
}

func (orderHandler *OrderHandler) getService(c *gin.Context) (OrderService, bool) {
	orderService, _, ok := orderHandler.getServiceAndConfig(c)
	return orderService, ok
}

func (orderHandler *OrderHandler) getServiceAndConfig(c *gin.Context) (OrderService, tenantdb.Config, bool) {
	cfgVal, ok := c.Get("tenantConfig")
	if !ok {
		httputil.WriteError(c, http.StatusInternalServerError, "tenant database configuration missing from context")
		return nil, tenantdb.Config{}, false
	}
	tenantCfg, ok := cfgVal.(tenantdb.Config)
	if !ok {
		httputil.WriteError(c, http.StatusInternalServerError, "invalid tenant database configuration type")
		return nil, tenantdb.Config{}, false
	}
	return orderHandler.factory(tenantCfg), tenantCfg, true
}

func (orderHandler *OrderHandler) getTxManager(cfg tenantdb.Config) TxManager {
	if orderHandler.txManagerFactory != nil {
		return orderHandler.txManagerFactory(cfg)
	}
	return txcontext.NewTxManager(cfg.DB)
}

func (orderHandler *OrderHandler) ListOrders(c *gin.Context) {
	orderService, ok := orderHandler.getService(c)
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

func (orderHandler *OrderHandler) CreateOrder(c *gin.Context) {
	orderService, tenantCfg, ok := orderHandler.getServiceAndConfig(c)
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

	var order *service.OrderOutput
	txManager := orderHandler.getTxManager(tenantCfg)
	err := txManager.WithTransaction(c.Request.Context(), func(txCtx context.Context) error {
		var err error
		order, err = orderService.CreateOrder(txCtx, input)
		return err
	})

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
