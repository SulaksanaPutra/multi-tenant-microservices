package mock

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"payment-service/internal/domain"
)

type MockProvider struct {
	id          domain.ProviderType
	secretKey   string
	shouldError bool
}

func NewMockProvider(id domain.ProviderType, secretKey string, shouldError bool) *MockProvider {
	if id == "" {
		id = domain.ProviderMock
	}
	if secretKey == "" {
		secretKey = "mock_secret_key"
	}
	return &MockProvider{
		id:          id,
		secretKey:   secretKey,
		shouldError: shouldError,
	}
}

func (p *MockProvider) ID() domain.ProviderType {
	return p.id
}

func (p *MockProvider) CreatePaymentSession(ctx context.Context, req domain.CreateSessionRequest) (*domain.PaymentSessionOutput, error) {
	if p.shouldError {
		return nil, domain.ErrProviderTransientFailure
	}

	secret := p.secretKey
	if req.Credentials.SecretKey != "" {
		secret = req.Credentials.SecretKey
	}

	extID := fmt.Sprintf("ext_%s_%s", p.id, req.PaymentID)
	return &domain.PaymentSessionOutput{
		Provider:          p.id,
		ExternalSessionID: extID,
		Instructions: domain.PaymentInstructions{
			Type:        domain.InstructionRedirectURL,
			RedirectURL: fmt.Sprintf("http://localhost:8000/mock-checkout?session_id=%s", extID),
			ExpiresAt:   time.Now().Add(24 * time.Hour),
		},
		RawProviderMetadata: map[string]string{
			"mock_mode":  "test",
			"secret_ref": secret[:min(4, len(secret))],
		},
	}, nil
}

func (p *MockProvider) VerifyWebhookSignature(ctx context.Context, headers map[string]string, body []byte) (*domain.WebhookEvent, error) {
	sig := headers["X-Webhook-Signature"]
	if sig == "" {
		sig = headers["x-webhook-signature"]
	}

	if sig != "" && sig != "mock_hmac_signature" {
		expectedSig := computeHMAC(body, p.secretKey)
		if sig != expectedSig {
			return nil, domain.ErrInvalidWebhookSignature
		}
	}

	var payload struct {
		EventID   string                 `json:"event_id"`
		EventType string                 `json:"event_type"`
		TenantID  string                 `json:"tenant_id"`
		OrderID   string                 `json:"order_id"`
		PaymentID string                 `json:"payment_id"`
		ExtID     string                 `json:"external_session_id"`
		Amount    float64                `json:"amount"`
		Currency  string                 `json:"currency"`
		RawData   map[string]interface{} `json:"raw_data"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode mock webhook payload: %w", err)
	}

	var evtType domain.WebhookEventType
	switch payload.EventType {
	case "payment.succeeded", "payment_intent.succeeded":
		evtType = domain.WebhookEventTypePaymentSucceeded
	case "payment.failed":
		evtType = domain.WebhookEventTypePaymentFailed
	case "payment.refunded":
		evtType = domain.WebhookEventTypePaymentRefunded
	default:
		evtType = domain.WebhookEventTypePaymentSucceeded
	}

	if payload.Currency == "" {
		payload.Currency = "USD"
	}

	return &domain.WebhookEvent{
		EventID:           payload.EventID,
		EventType:         evtType,
		Provider:          p.id,
		TenantID:          payload.TenantID,
		OrderID:           payload.OrderID,
		PaymentID:         payload.PaymentID,
		ExternalSessionID: payload.ExtID,
		Amount:            payload.Amount,
		Currency:          payload.Currency,
		Timestamp:         time.Now(),
		RawPayload:        payload.RawData,
	}, nil
}

func (p *MockProvider) CancelPaymentSession(ctx context.Context, externalSessionID string) error {
	// Mock cancellation always succeeds
	return nil
}

func computeHMAC(body []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
