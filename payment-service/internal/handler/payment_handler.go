package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"payment-service/internal/domain"
	"payment-service/internal/service"
)

type PaymentHandler struct {
	paymentService PaymentService
}

func NewPaymentHandler(paymentService PaymentService) *PaymentHandler {
	return &PaymentHandler{
		paymentService: paymentService,
	}
}

func (paymentHandler *PaymentHandler) GetPaymentByID(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		httputil.WriteError(c, http.StatusBadRequest, "payment id is required")
		return
	}

	p, err := paymentHandler.paymentService.GetPaymentByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrPaymentNotFound) {
			httputil.WriteError(c, http.StatusNotFound, "payment not found")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	tenantID, _ := c.Get(middleware.ContextKeyTenantID)
	if p.TenantID != tenantID.(string) {
		httputil.WriteError(c, http.StatusForbidden, "access denied: tenant mismatch")
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "payment retrieved successfully", p)
}

func (paymentHandler *PaymentHandler) GetPaymentByOrderID(c *gin.Context) {
	orderID := c.Param("orderID")
	if orderID == "" {
		httputil.WriteError(c, http.StatusBadRequest, "order id is required")
		return
	}

	rawTenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		httputil.WriteError(c, http.StatusUnauthorized, "unauthorized: tenant_id missing from context")
		return
	}
	tenantID := rawTenantID.(string)

	p, err := paymentHandler.paymentService.GetPaymentByOrderID(c.Request.Context(), tenantID, orderID)
	if err != nil {
		if errors.Is(err, domain.ErrPaymentNotFound) {
			httputil.WriteError(c, http.StatusNotFound, "payment for order not found")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "payment retrieved successfully", p)
}

func (paymentHandler *PaymentHandler) HandleWebhook(c *gin.Context) {
	provParam := c.Param("provider")
	if provParam == "" {
		httputil.WriteError(c, http.StatusBadRequest, "provider parameter is required")
		return
	}
	providerID := domain.ProviderType(provParam)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		httputil.WriteError(c, http.StatusBadRequest, "failed to read webhook request body")
		return
	}

	headers := make(map[string]string)
	for k, v := range c.Request.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}

	err = paymentHandler.paymentService.ProcessWebhook(c.Request.Context(), providerID, headers, body)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidWebhookSignature) {
			httputil.WriteError(c, http.StatusBadRequest, "invalid webhook signature")
			return
		}
		if errors.Is(err, domain.ErrPaymentAmountMismatch) {
			httputil.WriteError(c, http.StatusBadRequest, "payment amount or currency mismatch")
			return
		}
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			httputil.WriteError(c, http.StatusConflict, "invalid payment state transition")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "webhook processed successfully", gin.H{"processed": true})
}

type UpdatePSPConfigRequest struct {
	PriorityChain   []domain.ProviderType                              `json:"priority_chain" binding:"required"`
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials `json:"provider_configs"`
}

func (paymentHandler *PaymentHandler) UpdatePSPConfig(c *gin.Context) {
	rawTenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		httputil.WriteError(c, http.StatusUnauthorized, "unauthorized: tenant_id missing from context")
		return
	}
	tenantID := rawTenantID.(string)

	var req UpdatePSPConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	cfg := &domain.TenantPSPConfig{
		TenantID:        tenantID,
		PriorityChain:   req.PriorityChain,
		ProviderConfigs: req.ProviderConfigs,
	}

	if err := paymentHandler.paymentService.SavePSPConfig(c.Request.Context(), cfg); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "tenant PSP config updated successfully", gin.H{
		"tenant_id":      tenantID,
		"priority_chain": req.PriorityChain,
	})
}

func (paymentHandler *PaymentHandler) GetPSPConfig(c *gin.Context) {
	rawTenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		httputil.WriteError(c, http.StatusUnauthorized, "unauthorized: tenant_id missing from context")
		return
	}
	tenantID := rawTenantID.(string)

	cfg, err := paymentHandler.paymentService.GetPSPConfig(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	if cfg == nil {
		cfg = &service.TenantPSPConfigOutput{
			TenantID:        tenantID,
			PriorityChain:   []domain.ProviderType{domain.ProviderMock, domain.ProviderDirectBank},
			ProviderConfigs: make(map[domain.ProviderType]domain.ProviderCredentials),
		}
	}

	httputil.WriteSuccess(c, http.StatusOK, "tenant PSP config retrieved successfully", cfg)
}
