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

type PaymentMethodResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type PaymentMethodConfigRequest struct {
	ID            string   `json:"id" binding:"required"`
	Name          string   `json:"name" binding:"required"`
	Type          string   `json:"type" binding:"required"`
	Enabled       bool     `json:"enabled"`
	PriorityChain []string `json:"priority_chain" binding:"required"`
}

type ProviderCredentialsRequest struct {
	APIKey        string            `json:"api_key"`
	SecretKey     string            `json:"secret_key"`
	WebhookSecret string            `json:"webhook_secret"`
	ExtraOptions  map[string]string `json:"extra_options,omitempty"`
}

type PaymentMethodConfigResponse struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Enabled       bool     `json:"enabled"`
	PriorityChain []string `json:"priority_chain"`
}

type ProviderCredentialsResponse struct {
	APIKey        string            `json:"api_key"`
	SecretKey     string            `json:"secret_key"`
	WebhookSecret string            `json:"webhook_secret"`
	ExtraOptions  map[string]string `json:"extra_options,omitempty"`
}

type UpdatePSPConfigRequest struct {
	Methods         []PaymentMethodConfigRequest          `json:"methods" binding:"required"`
	ProviderConfigs map[string]ProviderCredentialsRequest `json:"provider_configs"`
}

type UpdatePSPConfigResponse struct {
	TenantID string                        `json:"tenant_id"`
	Methods  []PaymentMethodConfigResponse `json:"methods"`
}

type TenantPSPConfigResponse struct {
	TenantID        string                                 `json:"tenant_id"`
	Methods         []PaymentMethodConfigResponse          `json:"methods"`
	ProviderConfigs map[string]ProviderCredentialsResponse `json:"provider_configs"`
}

type WebhookResponse struct {
	Processed bool `json:"processed"`
}

func toPaymentResponse(paymentOutput *service.PaymentOutput) GetPaymentResponse {
	return GetPaymentResponse{
		ID:            paymentOutput.ID,
		DebtID:        paymentOutput.DebtID,
		TenantID:      paymentOutput.TenantID,
		OrderID:       paymentOutput.OrderID,
		Amount:        paymentOutput.Amount,
		Currency:      paymentOutput.Currency,
		Status:        string(paymentOutput.Status),
		Provider:      string(paymentOutput.Provider),
		PaymentMethod: paymentOutput.PaymentMethod,
		Instructions: PaymentInstructionsResponse{
			Type:         string(paymentOutput.Instructions.Type),
			RedirectURL:  paymentOutput.Instructions.RedirectURL,
			VANumber:     paymentOutput.Instructions.VANumber,
			BankCode:     paymentOutput.Instructions.BankCode,
			QRCodeString: paymentOutput.Instructions.QRCodeString,
			DeepLink:     paymentOutput.Instructions.DeepLink,
			ExpiresAt:    paymentOutput.Instructions.ExpiresAt,
		},
		CreatedAt: paymentOutput.CreatedAt,
		UpdatedAt: paymentOutput.UpdatedAt,
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

func (paymentHandler *PaymentHandler) ListAvailablePaymentMethods(c *gin.Context) {
	rawTenantID, exists := c.Get(middleware.ContextKeyTenantID)
	if !exists {
		httputil.WriteError(c, http.StatusUnauthorized, "unauthorized: tenant_id missing from context")
		return
	}
	tenantID := rawTenantID.(string)

	methods, err := paymentHandler.paymentProviderService.ListAvailablePaymentMethods(c.Request.Context(), tenantID)
	if err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	response := make([]PaymentMethodResponse, len(methods))
	for index, method := range methods {
		response[index] = PaymentMethodResponse{
			ID:   method.ID,
			Name: method.Name,
			Type: string(method.Type),
		}
	}

	httputil.WriteSuccess(c, http.StatusOK, "available payment methods retrieved successfully", response)
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

	initiatedPayment := initOut.Payment

	for _, cancelledSession := range initOut.CancelledSessions {
		if cancelledSession.ExternalSessionID != "" {
			_ = paymentHandler.paymentProviderService.CancelPaymentSession(c.Request.Context(), cancelledSession.Provider, cancelledSession.ExternalSessionID)
		}
	}

	execOut, execErr := paymentHandler.paymentProviderService.CreatePaymentSessionWithFallback(c.Request.Context(), service.CreatePaymentSessionWithFallbackInput{
		TenantID:      tenantID,
		PaymentID:     initiatedPayment.ID,
		OrderID:       initiatedPayment.OrderID,
		Amount:        initiatedPayment.Amount,
		Currency:      initiatedPayment.Currency,
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
			PaymentID:      initiatedPayment.ID,
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
		PaymentID:         initiatedPayment.ID,
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

	updatedPayment, err := paymentHandler.paymentService.GetPaymentByID(c.Request.Context(), initiatedPayment.ID)
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

	paymentOutput, err := paymentHandler.paymentService.GetPaymentByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrPaymentNotFound) {
			httputil.WriteError(c, http.StatusNotFound, "payment not found")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	tenantID, _ := c.Get(middleware.ContextKeyTenantID)
	if paymentOutput.TenantID != tenantID.(string) {
		httputil.WriteError(c, http.StatusForbidden, "access denied: tenant mismatch")
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "payment retrieved successfully", toPaymentResponse(paymentOutput))
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

	paymentOutput, err := paymentHandler.paymentService.GetPaymentByOrderID(c.Request.Context(), tenantID, orderID)
	if err != nil {
		if errors.Is(err, domain.ErrPaymentNotFound) {
			httputil.WriteError(c, http.StatusNotFound, "payment for order not found")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "payment retrieved successfully", toPaymentResponse(paymentOutput))
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

	debtOutput, err := paymentHandler.debtService.GetPayableDebtByOrderID(c.Request.Context(), tenantID, orderID)
	if err != nil {
		if errors.Is(err, domain.ErrDebtNotFound) {
			httputil.WriteError(c, http.StatusNotFound, "payable debt for order not found")
			return
		}
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	httputil.WriteSuccess(c, http.StatusOK, "payable debt retrieved successfully", GetPayableDebtResponse{
		ID:          debtOutput.ID,
		TenantID:    debtOutput.TenantID,
		OrderID:     debtOutput.OrderID,
		TotalAmount: debtOutput.TotalAmount,
		PaidAmount:  debtOutput.PaidAmount,
		Currency:    debtOutput.Currency,
		Status:      string(debtOutput.Status),
		CreatedAt:   debtOutput.CreatedAt,
		UpdatedAt:   debtOutput.UpdatedAt,
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
	for headerKey, headerValues := range c.Request.Header {
		if len(headerValues) > 0 {
			headers[headerKey] = headerValues[0]
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
		for _, attempt := range output.CancelledAttempts {
			_ = paymentHandler.paymentProviderService.CancelPaymentSession(c.Request.Context(), attempt.Provider, attempt.ExternalSessionID)
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

	methods := make([]domain.PaymentMethodConfig, len(req.Methods))
	for index, methodReq := range req.Methods {
		priorityChain := make([]domain.ProviderType, len(methodReq.PriorityChain))
		for chainIndex, providerTypeStr := range methodReq.PriorityChain {
			priorityChain[chainIndex] = domain.ProviderType(providerTypeStr)
		}
		methods[index] = domain.PaymentMethodConfig{
			ID:            methodReq.ID,
			Name:          methodReq.Name,
			Type:          domain.InstructionType(methodReq.Type),
			Enabled:       methodReq.Enabled,
			PriorityChain: priorityChain,
		}
	}

	providerConfigs := make(map[domain.ProviderType]domain.ProviderCredentials)
	for providerKey, credentialsReq := range req.ProviderConfigs {
		providerConfigs[domain.ProviderType(providerKey)] = domain.ProviderCredentials{
			APIKey:        credentialsReq.APIKey,
			SecretKey:     credentialsReq.SecretKey,
			WebhookSecret: credentialsReq.WebhookSecret,
			ExtraOptions:  credentialsReq.ExtraOptions,
		}
	}

	input := service.SavePSPConfigInput{
		TenantID:        tenantID,
		Methods:         methods,
		ProviderConfigs: providerConfigs,
	}

	if err := paymentHandler.pspConfigService.SaveConfig(c.Request.Context(), input); err != nil {
		httputil.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	methodsResponse := make([]PaymentMethodConfigResponse, len(req.Methods))
	for index, methodReq := range req.Methods {
		methodsResponse[index] = PaymentMethodConfigResponse{
			ID:            methodReq.ID,
			Name:          methodReq.Name,
			Type:          methodReq.Type,
			Enabled:       methodReq.Enabled,
			PriorityChain: methodReq.PriorityChain,
		}
	}

	httputil.WriteSuccess(c, http.StatusOK, "tenant PSP config updated successfully", UpdatePSPConfigResponse{
		TenantID: tenantID,
		Methods:  methodsResponse,
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

	methodsResponse := make([]PaymentMethodConfigResponse, len(cfg.Methods))
	for index, method := range cfg.Methods {
		chain := make([]string, len(method.PriorityChain))
		for chainIndex, providerType := range method.PriorityChain {
			chain[chainIndex] = string(providerType)
		}
		methodsResponse[index] = PaymentMethodConfigResponse{
			ID:            method.ID,
			Name:          method.Name,
			Type:          string(method.Type),
			Enabled:       method.Enabled,
			PriorityChain: chain,
		}
	}

	providerConfigsResponse := make(map[string]ProviderCredentialsResponse)
	for providerType, creds := range cfg.ProviderConfigs {
		providerConfigsResponse[string(providerType)] = ProviderCredentialsResponse{
			APIKey:        creds.APIKey,
			SecretKey:     creds.SecretKey,
			WebhookSecret: creds.WebhookSecret,
			ExtraOptions:  creds.ExtraOptions,
		}
	}

	httputil.WriteSuccess(c, http.StatusOK, "tenant PSP config retrieved successfully", TenantPSPConfigResponse{
		TenantID:        cfg.TenantID,
		Methods:         methodsResponse,
		ProviderConfigs: providerConfigsResponse,
	})
}
