package domain

import "time"

const (
	ExchangeCompanyEvents                  = "company.events"
	RoutingKeyOrderCreated                 = "order.created"
	RoutingKeyPaymentInstructionsGenerated = "payment.instructions_generated"
	RoutingKeyPaymentSucceeded            = "payment.succeeded"
	RoutingKeyPaymentFailed               = "payment.failed"
	RoutingKeyPaymentLateReceived          = "payment.late_payment_received"
	RoutingKeyPaymentExpired               = "payment.expired"
	QueuePaymentServiceOrderCreated        = "payment_service_order_created"
)

type OrderCreatedEvent struct {
	EventID    string  `json:"event_id"`
	TenantID   string  `json:"tenant_id"`
	OrderID    string  `json:"order_id"`
	CustomerID string  `json:"customer_id"`
	Amount     float64 `json:"amount"`
	Currency   string  `json:"currency"`
	Status     string  `json:"status"`
}

type PaymentInstructionsGeneratedEvent struct {
	EventID       string              `json:"event_id"`
	TenantID      string              `json:"tenant_id"`
	PaymentID     string              `json:"payment_id"`
	OrderID       string              `json:"order_id"`
	Amount        float64             `json:"amount"`
	Currency      string              `json:"currency"`
	PaymentMethod string              `json:"payment_method,omitempty"`
	Instructions  PaymentInstructions `json:"instructions"`
	Provider      ProviderType        `json:"provider"`
}

type PaymentSucceededEvent struct {
	EventID           string    `json:"event_id"`
	TenantID          string    `json:"tenant_id"`
	PaymentID         string    `json:"payment_id"`
	OrderID           string    `json:"order_id"`
	Amount            float64   `json:"amount"`
	Currency          string    `json:"currency"`
	Provider          ProviderType `json:"provider"`
	ExternalSessionID string    `json:"external_session_id"`
	SucceededAt       time.Time `json:"succeeded_at"`
}

type PaymentFailedEvent struct {
	EventID   string       `json:"event_id"`
	TenantID  string       `json:"tenant_id"`
	PaymentID string       `json:"payment_id"`
	OrderID   string       `json:"order_id"`
	Reason    string       `json:"reason"`
	Provider  ProviderType `json:"provider"`
}

type PaymentLateReceivedEvent struct {
	EventID           string       `json:"event_id"`
	TenantID          string       `json:"tenant_id"`
	PaymentID         string       `json:"payment_id"`
	OrderID           string       `json:"order_id"`
	Amount            float64      `json:"amount"`
	Currency          string       `json:"currency"`
	Provider          ProviderType `json:"provider"`
	ExternalSessionID string       `json:"external_session_id"`
	ReceivedAt        time.Time    `json:"received_at"`
	WebhookPayload    map[string]any `json:"webhook_payload"`
}

type PaymentExpiredEvent struct {
	EventID   string    `json:"event_id"`
	TenantID  string    `json:"tenant_id"`
	PaymentID string    `json:"payment_id"`
	OrderID   string    `json:"order_id"`
	ExpiredAt time.Time `json:"expired_at"`
}
