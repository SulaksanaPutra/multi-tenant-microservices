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
	ExecuteFallbackChain(ctx context.Context, req domain.CreateSessionRequest) (*provider.FallbackExecutionOutput, error)
	GetProvider(providerID domain.ProviderType) (domain.PaymentProvider, bool)
}

type PaymentProviderService struct {
	registry ProviderRegistry
	logger   *slog.Logger
}

func NewPaymentProviderService(
	registry ProviderRegistry,
	logger *slog.Logger,
) *PaymentProviderService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PaymentProviderService{
		registry: registry,
		logger:   logger,
	}
}

type ExecuteFallbackInput struct {
	TenantID    string
	PaymentID   string
	OrderID     string
	Amount      float64
	Currency    string
	Description string
	ReturnURL   string
}

type ExecuteFallbackOutput struct {
	Provider       domain.ProviderType
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

func (paymentProviderService *PaymentProviderService) ExecuteFallback(ctx context.Context, input ExecuteFallbackInput) (*ExecuteFallbackOutput, error) {
	req := domain.CreateSessionRequest{
		TenantID:    input.TenantID,
		PaymentID:   input.PaymentID,
		OrderID:     input.OrderID,
		Amount:      input.Amount,
		Currency:    input.Currency,
		Description: input.Description,
		ReturnURL:   input.ReturnURL,
	}

	execOut, err := paymentProviderService.registry.ExecuteFallbackChain(ctx, req)
	if execOut == nil {
		return nil, err
	}

	out := &ExecuteFallbackOutput{
		Provider:       execOut.Provider,
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
	adapter, ok := paymentProviderService.registry.GetProvider(providerID)
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
	adapter, ok := paymentProviderService.registry.GetProvider(providerID)
	if !ok {
		return fmt.Errorf("unregistered provider for cancel: %s", providerID)
	}

	return adapter.CancelPaymentSession(ctx, externalSessionID)
}
