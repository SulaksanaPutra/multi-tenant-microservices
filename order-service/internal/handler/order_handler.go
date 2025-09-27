package handler

import (
	"net/http"

	"order-service/internal/service"
	"order-service/internal/utils"

	"github.com/gin-gonic/gin"
)

type CreateOrderRequest struct {
	CustomerID string  `json:"customer_id" binding:"required"`
	Amount     float64 `json:"amount" binding:"required,gt=0"`
	Status     string  `json:"status"`
}

type OrderHandler struct {
	orderService service.OrderService
}

func NewOrderHandler(orderService service.OrderService) *OrderHandler {
	return &OrderHandler{orderService: orderService}
}

func (h *OrderHandler) GetOrders(c *gin.Context) {
	tenantID := c.GetHeader("X-Tenant-ID")
	if tenantID == "" {
		utils.WriteError(c, http.StatusBadRequest, "X-Tenant-ID header is required")
		return
	}

	orders, err := h.orderService.GetOrders(c.Request.Context(), tenantID)
	if err != nil {
		utils.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteSuccess(c, http.StatusOK, "", orders)
}

func (h *OrderHandler) CreateOrder(c *gin.Context) {
	tenantID := c.GetHeader("X-Tenant-ID")
	if tenantID == "" {
		utils.WriteError(c, http.StatusBadRequest, "X-Tenant-ID header is required")
		return
	}

	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.WriteError(c, http.StatusBadRequest, err.Error())
		return
	}

	input := service.CreateOrderInput{
		TenantID:   tenantID,
		CustomerID: req.CustomerID,
		Amount:     req.Amount,
		Status:     req.Status,
	}

	order, err := h.orderService.CreateOrder(c.Request.Context(), input)
	if err != nil {
		utils.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	utils.WriteSuccess(c, http.StatusCreated, "Order created successfully", order)
}
