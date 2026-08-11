package domain_test

import (
	"testing"

	"payment-service/internal/domain"
)

func TestSentinelErrors(t *testing.T) {
	sentinels := map[string]error{
		"ErrPaymentNotFound":          domain.ErrPaymentNotFound,
		"ErrInvalidStatusTransition":  domain.ErrInvalidStatusTransition,
		"ErrPaymentAmountMismatch":    domain.ErrPaymentAmountMismatch,
		"ErrInvalidWebhookSignature":  domain.ErrInvalidWebhookSignature,
		"ErrNoAvailableProvider":      domain.ErrNoAvailableProvider,
		"ErrProviderTransientFailure": domain.ErrProviderTransientFailure,
		"ErrDuplicateEvent":           domain.ErrDuplicateEvent,
		"ErrInvalidCiphertext":        domain.ErrInvalidCiphertext,
		"ErrEmptyMasterKey":           domain.ErrEmptyMasterKey,
		"ErrCircuitOpen":              domain.ErrCircuitOpen,
	}

	for name, err := range sentinels {
		if err == nil {
			t.Fatalf("expected sentinel error '%s' to be non-nil", name)
		}
		if err.Error() == "" {
			t.Errorf("sentinel error '%s' has empty message", name)
		}
	}
}
