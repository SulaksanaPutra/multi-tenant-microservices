package handler

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"payment-service/internal/domain"
	"payment-service/internal/service"

	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
)

type PaymentInstructionsResponse struct {
	Type         string    `json:"type"`
	RedirectURL  string    `json:"redirect_url,omitempty"`
	VANumber     string    `json:"va_number,omitempty"`
	BankCode     string    `json:"bank_code,omitempty"`
	QRCodeString string    `json:"qr_code_string,omitempty"`
	DeepLink     string    `json:"deep_link,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type GetPaymentResponse struct {
	ID           string                      `json:"id"`
	TenantID     string                      `json:"tenant_id"`
	OrderID      string                      `json:"order_id"`
	Amount       float64                     `json:"amount"`
	Currency     string                      `json:"currency"`
	Status       string                      `json:"status"`
	Provider     string                      `json:"provider"`
	Instructions PaymentInstructionsResponse `json:"instructions"`
	CreatedAt    time.Time                   `json:"created_at"`
	UpdatedAt    time.Time                   `json:"updated_at"`
}

type UpdatePSPConfigRequest struct {
	PriorityChain   []domain.ProviderType                              `json:"priority_chain" binding:"required"`
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials `json:"provider_configs"`
}

type UpdatePSPConfigResponse struct {
	TenantID      string                `json:"tenant_id"`
	PriorityChain []domain.ProviderType `json:"priority_chain"`
}

type TenantPSPConfigResponse struct {
	TenantID        string                                             `json:"tenant_id"`
	PriorityChain   []domain.ProviderType                              `json:"priority_chain"`
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials `json:"provider_configs"`
}

type WebhookResponse struct {
	Processed bool `json:"processed"`
}

func toPaymentResponse(p *service.PaymentOutput) GetPaymentResponse {
	return GetPaymentResponse{
		ID:       p.ID,
		TenantID: p.TenantID,
		OrderID:  p.OrderID,
		Amount:   p.Amount,
		Currency: p.Currency,
		Status:   string(p.Status),
		Provider: string(p.Provider),
		Instructions: PaymentInstructionsResponse{
			Type:         string(p.Instructions.Type),
			RedirectURL:  p.Instructions.RedirectURL,
			VANumber:     p.Instructions.VANumber,
			BankCode:     p.Instructions.BankCode,
			QRCodeString: p.Instructions.QRCodeString,
			DeepLink:     p.Instructions.DeepLink,
			ExpiresAt:    p.Instructions.ExpiresAt,
		},
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
}

type PaymentHandler struct {
	paymentService         PaymentService
	paymentProviderService PaymentProviderService
	pspConfigService       PSPConfigService
}

func NewPaymentHandler(
	paymentService PaymentService,
	paymentProviderService PaymentProviderService,
	pspConfigService PSPConfigService,
) *PaymentHandler {
	return &PaymentHandler{
		paymentService:         paymentService,
		paymentProviderService: paymentProviderService,
		pspConfigService:       pspConfigService,
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

	httputil.WriteSuccess(c, http.StatusOK, "payment retrieved successfully", toPaymentResponse(p))
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

	httputil.WriteSuccess(c, http.StatusOK, "payment retrieved successfully", toPaymentResponse(p))
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

	webhookEvt, err := paymentHandler.paymentProviderService.VerifyWebhookSignature(c.Request.Context(), providerID, headers, body)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidWebhookSignature) {
			httputil.WriteError(c, http.StatusBadRequest, "invalid webhook signature")
			return
		}
		httputil.WriteError(c, http.StatusBadRequest, err.Error())
		return
	}

	output, err := paymentHandler.paymentService.ProcessVerifiedWebhook(c.Request.Context(), service.ProcessVerifiedWebhookInput{
		EventID:           webhookEvt.EventID,
		EventType:         webhookEvt.EventType,
		Provider:          webhookEvt.Provider,
		TenantID:          webhookEvt.TenantID,
		OrderID:           webhookEvt.OrderID,
		PaymentID:         webhookEvt.PaymentID,
		ExternalSessionID: webhookEvt.ExternalSessionID,
		Amount:            webhookEvt.Amount,
		Currency:          webhookEvt.Currency,
		RawPayload:        webhookEvt.RawPayload,
	})
	if err != nil {
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

	if output != nil && len(output.CancelledAttempts) > 0 {
		for _, att := range output.CancelledAttempts {
			_ = paymentHandler.paymentProviderService.CancelPaymentSession(c.Request.Context(), att.Provider, att.ExternalSessionID)
		}
	}

	httputil.WriteSuccess(c, http.StatusOK, "webhook processed successfully", WebhookResponse{Processed: true})
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

	input := service.SavePSPConfigInput{
		TenantID:        tenantID,
		PriorityChain:   req.PriorityChain,
		ProviderConfigs: req.ProviderConfigs,
	}

	if err := paymentHandler.pspConfigService.SaveConfig(c.Request.Context(), input); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "tenant PSP config updated successfully", UpdatePSPConfigResponse{
		TenantID:      tenantID,
		PriorityChain: req.PriorityChain,
	})
}

func (paymentHandler *PaymentHandler) GetPSPConfig(c *gin.Context) {
	rawTenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		httputil.WriteError(c, http.StatusUnauthorized, "unauthorized: tenant_id missing from context")
		return
	}
	tenantID := rawTenantID.(string)

	cfg, err := paymentHandler.pspConfigService.GetConfig(c.Request.Context(), tenantID)
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

	httputil.WriteSuccess(c, http.StatusOK, "tenant PSP config retrieved successfully", TenantPSPConfigResponse{
		TenantID:        cfg.TenantID,
		PriorityChain:   cfg.PriorityChain,
		ProviderConfigs: cfg.ProviderConfigs,
	})
}
