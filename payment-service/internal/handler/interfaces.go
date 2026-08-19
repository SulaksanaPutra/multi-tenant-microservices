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
	InitiatePaymentSession(ctx context.Context, input service.InitiatePaymentSessionInput) (*service.InitiatePaymentSessionOutput, error)
	CompleteInstructionGeneration(ctx context.Context, input service.CompleteInstructionInput) error
	FailInstructionGeneration(ctx context.Context, input service.FailInstructionInput) error
	ListAttemptsByPaymentID(ctx context.Context, paymentID string) ([]*service.PaymentAttemptOutput, error)
	ProcessVerifiedWebhook(ctx context.Context, input service.ProcessVerifiedWebhookInput) (*service.ProcessWebhookOutput, error)
}

// DebtService is the handler-side interface for payable debt querying.
type DebtService interface {
	GetPayableDebtByOrderID(ctx context.Context, tenantID, orderID string) (*service.PayableDebtOutput, error)
	GetPayableDebtByID(ctx context.Context, id string) (*service.PayableDebtOutput, error)
}

type PaymentProviderService interface {
	ListAvailablePaymentMethods(ctx context.Context, tenantID string) ([]service.PaymentMethodOutput, error)
	ExecuteFallback(ctx context.Context, input service.ExecuteFallbackInput) (*service.ExecuteFallbackOutput, error)
	VerifyWebhookSignature(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) (*service.VerifyWebhookOutput, error)
	CancelPaymentSession(ctx context.Context, providerID domain.ProviderType, externalSessionID string) error
}

type PSPConfigService interface {
	SaveConfig(ctx context.Context, input service.SavePSPConfigInput) error
	GetConfig(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error)
}
