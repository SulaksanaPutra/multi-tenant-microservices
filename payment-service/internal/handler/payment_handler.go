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
	ID            string                      `json:"id"`
	DebtID        string                      `json:"debt_id,omitempty"`
	TenantID      string                      `json:"tenant_id"`
	OrderID       string                      `json:"order_id"`
	Amount        float64                     `json:"amount"`
	Currency      string                      `json:"currency"`
	Status        string                      `json:"status"`
	Provider      string                      `json:"provider"`
	PaymentMethod string                      `json:"payment_method,omitempty"`
	Instructions  PaymentInstructionsResponse `json:"instructions"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

type GetPayableDebtResponse struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	OrderID     string    `json:"order_id"`
	TotalAmount float64   `json:"total_amount"`
	PaidAmount  float64   `json:"paid_amount"`
	Currency    string    `json:"currency"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type InitiatePaymentSessionRequest struct {
	OrderID       string  `json:"order_id" binding:"required"`
	PaymentMethod string  `json:"payment_method"`
	Amount        float64 `json:"amount,omitempty"`
}

type UpdatePSPConfigRequest struct {
	Methods         []domain.PaymentMethodConfig                       `json:"methods" binding:"required"`
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials `json:"provider_configs"`
}

type UpdatePSPConfigResponse struct {
	TenantID string                       `json:"tenant_id"`
	Methods  []domain.PaymentMethodConfig `json:"methods"`
}

type TenantPSPConfigResponse struct {
	TenantID        string                                             `json:"tenant_id"`
	Methods         []domain.PaymentMethodConfig                       `json:"methods"`
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials `json:"provider_configs"`
}

type WebhookResponse struct {
	Processed bool `json:"processed"`
}

func toPaymentResponse(p *service.PaymentOutput) GetPaymentResponse {
	return GetPaymentResponse{
		ID:            p.ID,
		DebtID:        p.DebtID,
		TenantID:      p.TenantID,
		OrderID:       p.OrderID,
		Amount:        p.Amount,
		Currency:      p.Currency,
		Status:        string(p.Status),
		Provider:      string(p.Provider),
		PaymentMethod: p.PaymentMethod,
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
	debtService            DebtService
	paymentProviderService PaymentProviderService
	pspConfigService       PSPConfigService
}

func NewPaymentHandler(
	paymentService PaymentService,
	debtService DebtService,
	paymentProviderService PaymentProviderService,
	pspConfigService PSPConfigService,
) *PaymentHandler {
	return &PaymentHandler{
		paymentService:         paymentService,
		debtService:            debtService,
		paymentProviderService: paymentProviderService,
		pspConfigService:       pspConfigService,
	}
}

func (paymentHandler *PaymentHandler) GetAvailablePaymentMethods(c *gin.Context) {
	rawTenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		httputil.WriteError(c, http.StatusUnauthorized, "unauthorized: tenant_id missing from context")
		return
	}
	tenantID := rawTenantID.(string)

	methods, err := paymentHandler.paymentProviderService.GetAvailablePaymentMethods(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "available payment methods retrieved successfully", methods)
}

func (paymentHandler *PaymentHandler) InitiatePayment(c *gin.Context) {
	rawTenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		httputil.WriteError(c, http.StatusUnauthorized, "unauthorized: tenant_id missing from context")
		return
	}
	tenantID := rawTenantID.(string)

	var req InitiatePaymentSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteValidationError(c, err)
		return
	}

	initOut, err := paymentHandler.paymentService.InitiatePaymentSession(c.Request.Context(), service.InitiatePaymentSessionInput{
		TenantID:      tenantID,
		OrderID:       req.OrderID,
		Amount:        req.Amount,
		PaymentMethod: req.PaymentMethod,
	})
	if err != nil {
		if errors.Is(err, domain.ErrDebtNotFound) {
			httputil.WriteError(c, http.StatusNotFound, "payable debt for order not found")
			return
		}
		if errors.Is(err, domain.ErrDebtAlreadyPaid) {
			httputil.WriteError(c, http.StatusConflict, "order is already fully paid")
			return
		}
		if errors.Is(err, domain.ErrInvalidStatusTransition) {
			httputil.WriteError(c, http.StatusConflict, "cannot initiate payment for expired or cancelled order")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	p := initOut.Payment

	for _, cs := range initOut.CancelledSessions {
		if cs.ExternalSessionID != "" {
			_ = paymentHandler.paymentProviderService.CancelPaymentSession(c.Request.Context(), cs.Provider, cs.ExternalSessionID)
		}
	}

	execOut, execErr := paymentHandler.paymentProviderService.ExecuteFallback(c.Request.Context(), service.ExecuteFallbackInput{
		TenantID:      tenantID,
		PaymentID:     p.ID,
		OrderID:       p.OrderID,
		Amount:        p.Amount,
		Currency:      p.Currency,
		PaymentMethod: req.PaymentMethod,
	})

	if execErr != nil || execOut == nil || execOut.Session == nil {
		failedAttempts := []domain.ProviderType{}
		attemptErrors := map[domain.ProviderType]error{}
		errMsg := "no available payment provider"
		if execErr != nil {
			errMsg = execErr.Error()
		}
		if execOut != nil {
			failedAttempts = execOut.FailedAttempts
			attemptErrors = execOut.AttemptErrors
		}

		_ = paymentHandler.paymentService.FailInstructionGeneration(c.Request.Context(), service.FailInstructionInput{
			PaymentID:      p.ID,
			Reason:         errMsg,
			FailedAttempts: failedAttempts,
			AttemptErrors:  attemptErrors,
		})

		if errors.Is(execErr, domain.ErrInvalidPaymentMethod) {
			httputil.WriteError(c, http.StatusBadRequest, "invalid or unsupported payment method")
			return
		}
		httputil.WriteError(c, http.StatusBadGateway, errMsg)
		return
	}

	if err := paymentHandler.paymentService.CompleteInstructionGeneration(c.Request.Context(), service.CompleteInstructionInput{
		PaymentID:         p.ID,
		Provider:          execOut.Provider,
		PaymentMethod:     execOut.PaymentMethod,
		ExternalSessionID: execOut.Session.ExternalSessionID,
		Instructions:      execOut.Session.Instructions,
		FailedAttempts:    execOut.FailedAttempts,
		AttemptErrors:     execOut.AttemptErrors,
	}); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	updatedPayment, err := paymentHandler.paymentService.GetPaymentByID(c.Request.Context(), p.ID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "payment session initiated successfully", toPaymentResponse(updatedPayment))
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

func (paymentHandler *PaymentHandler) GetPayableDebtByOrderID(c *gin.Context) {
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

	d, err := paymentHandler.debtService.GetPayableDebtByOrderID(c.Request.Context(), tenantID, orderID)
	if err != nil {
		if errors.Is(err, domain.ErrDebtNotFound) {
			httputil.WriteError(c, http.StatusNotFound, "payable debt for order not found")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "payable debt retrieved successfully", GetPayableDebtResponse{
		ID:          d.ID,
		TenantID:    d.TenantID,
		OrderID:     d.OrderID,
		TotalAmount: d.TotalAmount,
		PaidAmount:  d.PaidAmount,
		Currency:    d.Currency,
		Status:      string(d.Status),
		CreatedAt:   d.CreatedAt,
		UpdatedAt:   d.UpdatedAt,
	})
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
		Methods:         req.Methods,
		ProviderConfigs: req.ProviderConfigs,
	}

	if err := paymentHandler.pspConfigService.SaveConfig(c.Request.Context(), input); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "tenant PSP config updated successfully", UpdatePSPConfigResponse{
		TenantID: tenantID,
		Methods:  req.Methods,
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
			Methods:         []domain.PaymentMethodConfig{},
			ProviderConfigs: make(map[domain.ProviderType]domain.ProviderCredentials),
		}
	}

	httputil.WriteSuccess(c, http.StatusOK, "tenant PSP config retrieved successfully", TenantPSPConfigResponse{
		TenantID:        cfg.TenantID,
		Methods:         cfg.Methods,
		ProviderConfigs: cfg.ProviderConfigs,
	})
}
