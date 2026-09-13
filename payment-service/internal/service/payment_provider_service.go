package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/provider"
)

type ProviderRegistry interface {
	CreatePaymentSessionWithFallback(ctx context.Context, cfg *domain.TenantPSPConfig, req domain.CreateSessionRequest, methodID string) (*provider.FallbackExecutionOutput, error)
	IsHealthy(providerID domain.ProviderType) bool
	GetProvider(providerID domain.ProviderType) (domain.PaymentProvider, bool)
}

type PaymentProviderService struct {
	providerRegistry    ProviderRegistry
	pspConfigRepository PSPConfigRepository
	logger              *slog.Logger
}

func NewPaymentProviderService(
	providerRegistry ProviderRegistry,
	pspConfigRepository PSPConfigRepository,
	logger *slog.Logger,
) *PaymentProviderService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PaymentProviderService{
		providerRegistry:    providerRegistry,
		pspConfigRepository: pspConfigRepository,
		logger:              logger,
	}
}

type PaymentMethodOutput struct {
	ID   string
	Name string
	Type domain.InstructionType
}

type CreatePaymentSessionWithFallbackInput struct {
	TenantID      string
	PaymentID     string
	OrderID       string
	Amount        float64
	Currency      string
	Description   string
	ReturnURL     string
	PaymentMethod string
}

type CreatePaymentSessionWithFallbackOutput struct {
	Provider       domain.ProviderType
	PaymentMethod  string
	Session        *domain.PaymentSessionOutput
	FailedAttempts []domain.ProviderType
	AttemptErrors  map[domain.ProviderType]error
}

type VerifyWebhookOutput struct {
	EventID           string
	EventType         domain.WebhookEventType
	Provider          domain.ProviderType
	TenantID          string
	OrderID           string
	PaymentID         string
	ExternalSessionID string
	Amount            float64
	Currency          string
	Timestamp         time.Time
	RawPayload        map[string]any
}

func (paymentProviderService *PaymentProviderService) ListAvailablePaymentMethods(ctx context.Context, tenantID string) ([]PaymentMethodOutput, error) {
	cfg, err := paymentProviderService.pspConfigRepository.FindByTenantID(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tenant PSP config: %w", err)
	}

	var outputs []PaymentMethodOutput
	for _, method := range cfg.Methods {
		if !method.Enabled {
			continue
		}

		hasHealthyProvider := false
		for _, providerID := range method.PriorityChain {
			if paymentProviderService.providerRegistry.IsHealthy(providerID) {
				hasHealthyProvider = true
				break
			}
		}

		if hasHealthyProvider {
			outputs = append(outputs, PaymentMethodOutput{
				ID:   method.ID,
				Name: method.Name,
				Type: method.Type,
			})
		}
	}

	return outputs, nil
}

func (paymentProviderService *PaymentProviderService) CreatePaymentSessionWithFallback(ctx context.Context, input CreatePaymentSessionWithFallbackInput) (*CreatePaymentSessionWithFallbackOutput, error) {
	cfg, err := paymentProviderService.pspConfigRepository.FindByTenantID(ctx, input.TenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tenant PSP config: %w", err)
	}

	req := domain.CreateSessionRequest{
		TenantID:      input.TenantID,
		PaymentID:     input.PaymentID,
		OrderID:       input.OrderID,
		Amount:        input.Amount,
		Currency:      input.Currency,
		Description:   input.Description,
		ReturnURL:     input.ReturnURL,
		PaymentMethod: input.PaymentMethod,
	}

	execOut, err := paymentProviderService.providerRegistry.CreatePaymentSessionWithFallback(ctx, cfg, req, input.PaymentMethod)
	if execOut == nil {
		return nil, err
	}

	out := &CreatePaymentSessionWithFallbackOutput{
		Provider:       execOut.Provider,
		PaymentMethod:  execOut.PaymentMethod,
		Session:        execOut.Session,
		FailedAttempts: execOut.FailedAttempts,
		AttemptErrors:  execOut.AttemptErrors,
	}

	return out, err
}

func (paymentProviderService *PaymentProviderService) VerifyWebhookSignature(
	ctx context.Context,
	providerID domain.ProviderType,
	headers map[string]string,
	body []byte,
) (*VerifyWebhookOutput, error) {
	adapter, ok := paymentProviderService.providerRegistry.GetProvider(providerID)
	if !ok {
		return nil, fmt.Errorf("unregistered provider for webhook: %s", providerID)
	}

	webhookEvt, err := adapter.VerifyWebhookSignature(ctx, headers, body)
	if err != nil {
		return nil, fmt.Errorf("webhook signature verification failed: %w", err)
	}

	return &VerifyWebhookOutput{
		EventID:           webhookEvt.EventID,
		EventType:         webhookEvt.EventType,
		Provider:          webhookEvt.Provider,
		TenantID:          webhookEvt.TenantID,
		OrderID:           webhookEvt.OrderID,
		PaymentID:         webhookEvt.PaymentID,
		ExternalSessionID: webhookEvt.ExternalSessionID,
		Amount:            webhookEvt.Amount,
		Currency:          webhookEvt.Currency,
		Timestamp:         webhookEvt.Timestamp,
		RawPayload:        webhookEvt.RawPayload,
	}, nil
}

func (paymentProviderService *PaymentProviderService) CancelPaymentSession(
	ctx context.Context,
	providerID domain.ProviderType,
	externalSessionID string,
) error {
	adapter, ok := paymentProviderService.providerRegistry.GetProvider(providerID)
	if !ok {
		return fmt.Errorf("unregistered provider for cancel: %s", providerID)
	}

	return adapter.CancelPaymentSession(ctx, externalSessionID)
}
