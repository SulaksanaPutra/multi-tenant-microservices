package handler

import (
	"net/http"

	"order-service/internal/service"
	"order-service/internal/utils"

	"github.com/gin-gonic/gin"
)

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
