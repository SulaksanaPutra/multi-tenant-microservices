package handler

import (
	"context"

	"payment-service/internal/domain"
	"payment-service/internal/service"
)

// =============================================================================
// Domain Service Contracts
// =============================================================================

// PaymentService is the handler-side interface for payment lifecycle operations.
type PaymentService interface {
	GetPaymentByID(ctx context.Context, id string) (*service.PaymentOutput, error)
	GetPaymentByOrderID(ctx context.Context, tenantID, orderID string) (*service.PaymentOutput, error)
	ProcessVerifiedWebhook(ctx context.Context, input service.ProcessVerifiedWebhookInput) (*service.ProcessWebhookOutput, error)
}

// PaymentProviderService is the handler-side interface for external gateway I/O and signature verification.
type PaymentProviderService interface {
	VerifyWebhookSignature(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) (*service.VerifyWebhookOutput, error)
	CancelPaymentSession(ctx context.Context, providerID domain.ProviderType, externalSessionID string) error
}

// PSPConfigService is the handler-side interface for tenant payment gateway configurations.
type PSPConfigService interface {
	SaveConfig(ctx context.Context, input service.SavePSPConfigInput) error
	GetConfig(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error)
}
