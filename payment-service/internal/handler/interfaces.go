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
	ProcessWebhook(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) error
	SavePSPConfig(ctx context.Context, config *domain.TenantPSPConfig) error
	GetPSPConfig(ctx context.Context, tenantID string) (*service.TenantPSPConfigOutput, error)
}
