package domain_test

import (
	"testing"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/testutil"
)

func TestOrderCreatedEvent_JSON(t *testing.T) {
	evt := domain.OrderCreatedEvent{
		EventID:    "evt_1",
		TenantID:   "tnt_1",
		OrderID:    "ord_1",
		CustomerID: "cust_1",
		Amount:     100.50,
		Status:     "PENDING_PAYMENT",
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("OrderCreatedEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestPaymentInstructionsGeneratedEvent_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	evt := domain.PaymentInstructionsGeneratedEvent{
		EventID:   "evt_2",
		TenantID:  "tnt_1",
		PaymentID: "pay_1",
		OrderID:   "ord_1",
		Amount:    250.00,
		Currency:  "USD",
		Instructions: domain.PaymentInstructions{
			Type:        domain.InstructionVirtualAccount,
			VANumber:    "88012345",
			BankCode:    "BCA",
			ExpiresAt:   now,
		},
		Provider: domain.ProviderDirectBank,
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled.PaymentID != evt.PaymentID || unmarshaled.Instructions.VANumber != evt.Instructions.VANumber {
		t.Errorf("PaymentInstructionsGeneratedEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestPaymentSucceededEvent_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	evt := domain.PaymentSucceededEvent{
		EventID:           "evt_3",
		TenantID:          "tnt_1",
		PaymentID:         "pay_1",
		OrderID:           "ord_1",
		Amount:            250.00,
		Currency:          "USD",
		Provider:          domain.ProviderMock,
		ExternalSessionID: "ext_123",
		SucceededAt:       now,
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled.PaymentID != evt.PaymentID || !unmarshaled.SucceededAt.Equal(evt.SucceededAt) {
		t.Errorf("PaymentSucceededEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestPaymentFailedEvent_JSON(t *testing.T) {
	evt := domain.PaymentFailedEvent{
		EventID:   "evt_4",
		TenantID:  "tnt_1",
		PaymentID: "pay_1",
		OrderID:   "ord_1",
		Reason:    "insufficient funds",
		Provider:  domain.ProviderMock,
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled != evt {
		t.Errorf("PaymentFailedEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestPaymentLateReceivedEvent_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	evt := domain.PaymentLateReceivedEvent{
		EventID:           "evt_5",
		TenantID:          "tnt_1",
		PaymentID:         "pay_1",
		OrderID:           "ord_1",
		Amount:            150.00,
		Currency:          "USD",
		Provider:          domain.ProviderStripe,
		ExternalSessionID: "ext_stripe_99",
		ReceivedAt:        now,
		WebhookPayload:    map[string]any{"raw": "payload"},
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled.PaymentID != evt.PaymentID || !unmarshaled.ReceivedAt.Equal(evt.ReceivedAt) {
		t.Errorf("PaymentLateReceivedEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}

func TestPaymentExpiredEvent_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	evt := domain.PaymentExpiredEvent{
		EventID:   "evt_6",
		TenantID:  "tnt_1",
		PaymentID: "pay_1",
		OrderID:   "ord_1",
		ExpiredAt: now,
	}

	unmarshaled := testutil.AssertJSONRoundtrip(t, evt)
	if unmarshaled.PaymentID != evt.PaymentID || !unmarshaled.ExpiredAt.Equal(evt.ExpiredAt) {
		t.Errorf("PaymentExpiredEvent mismatch: %+v vs %+v", unmarshaled, evt)
	}
}
