package directbank

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"payment-service/internal/domain"
)

type DirectBankProvider struct {
	bankCode string
}

func NewDirectBankProvider(bankCode string) *DirectBankProvider {
	if bankCode == "" {
		bankCode = "BCA"
	}
	return &DirectBankProvider{bankCode: bankCode}
}

func (p *DirectBankProvider) ID() domain.ProviderType {
	return domain.ProviderDirectBank
}

func (p *DirectBankProvider) CreatePaymentSession(ctx context.Context, req domain.CreateSessionRequest) (*domain.PaymentSessionResult, error) {
	extID := fmt.Sprintf("va_%s_%s", p.bankCode, req.PaymentID)
	vaNumber := fmt.Sprintf("88012%s", req.PaymentID)
	if len(vaNumber) > 16 {
		vaNumber = vaNumber[:16]
	}

	return &domain.PaymentSessionResult{
		Provider:          domain.ProviderDirectBank,
		ExternalSessionID: extID,
		Instructions: domain.PaymentInstructions{
			Type:      domain.InstructionVirtualAccount,
			VANumber:  vaNumber,
			BankCode:  p.bankCode,
			ExpiresAt: time.Now().Add(24 * time.Hour),
		},
		RawProviderMetadata: map[string]string{
			"bank_code": p.bankCode,
			"va_number": vaNumber,
		},
	}, nil
}

func (p *DirectBankProvider) VerifyWebhookSignature(ctx context.Context, headers map[string]string, body []byte) (*domain.WebhookEvent, error) {
	var payload struct {
		EventID   string  `json:"event_id"`
		EventType string  `json:"event_type"`
		TenantID  string  `json:"tenant_id"`
		OrderID   string  `json:"order_id"`
		PaymentID string  `json:"payment_id"`
		ExtID     string  `json:"external_session_id"`
		Amount    float64 `json:"amount"`
		Currency  string  `json:"currency"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode direct bank webhook: %w", err)
	}

	if payload.Currency == "" {
		payload.Currency = "USD"
	}

	return &domain.WebhookEvent{
		EventID:           payload.EventID,
		EventType:         domain.WebhookEventTypePaymentSucceeded,
		Provider:          domain.ProviderDirectBank,
		TenantID:          payload.TenantID,
		OrderID:           payload.OrderID,
		PaymentID:         payload.PaymentID,
		ExternalSessionID: payload.ExtID,
		Amount:            payload.Amount,
		Currency:          payload.Currency,
		Timestamp:         time.Now(),
	}, nil
}

func (p *DirectBankProvider) CancelPaymentSession(ctx context.Context, externalSessionID string) error {
	return nil
}
