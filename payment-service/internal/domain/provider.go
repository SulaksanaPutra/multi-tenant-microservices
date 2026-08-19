package domain

import (
	"context"
	"time"
)

type ProviderType string

const (
	ProviderStripe     ProviderType = "stripe"
	ProviderXendit     ProviderType = "xendit"
	ProviderMidtrans   ProviderType = "midtrans"
	ProviderDirectBank ProviderType = "direct_bank"
	ProviderMock       ProviderType = "mock"
)

type CreateSessionRequest struct {
	TenantID    string
	PaymentID   string
	OrderID     string
	Amount      float64
	Currency    string
	Description string
	ReturnURL   string
	Credentials ProviderCredentials
}

type PaymentSessionOutput struct {
	Provider            ProviderType
	ExternalSessionID   string
	Instructions        PaymentInstructions
	RawProviderMetadata map[string]string
}

type WebhookEventType string

const (
	WebhookEventTypePaymentSucceeded WebhookEventType = "payment.succeeded"
	WebhookEventTypePaymentFailed    WebhookEventType = "payment.failed"
	WebhookEventTypePaymentRefunded  WebhookEventType = "payment.refunded"
)

type WebhookEvent struct {
	EventID           string
	EventType         WebhookEventType
	Provider          ProviderType
	TenantID          string
	OrderID           string
	PaymentID         string
	ExternalSessionID string
	Amount            float64
	Currency          string
	Timestamp         time.Time
	RawPayload        map[string]any
}

type PaymentProvider interface {
	ID() ProviderType
	CreatePaymentSession(ctx context.Context, req CreateSessionRequest) (*PaymentSessionOutput, error)
	VerifyWebhookSignature(ctx context.Context, headers map[string]string, body []byte) (*WebhookEvent, error)
	CancelPaymentSession(ctx context.Context, externalSessionID string) error
}
