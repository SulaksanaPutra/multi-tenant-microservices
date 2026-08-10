package domain

import (
	"testing"
)

func TestValidateStateTransition(t *testing.T) {
	tests := []struct {
		name    string
		current PaymentStatus
		target  PaymentStatus
		wantErr bool
	}{
		{
			name:    "Pending to PaymentInstructionsReady",
			current: PaymentStatusPending,
			target:  PaymentStatusInstructionsReady,
			wantErr: false,
		},
		{
			name:    "PaymentInstructionsReady to Succeeded",
			current: PaymentStatusInstructionsReady,
			target:  PaymentStatusSucceeded,
			wantErr: false,
		},
		{
			name:    "Expired to RequiresManualReview (Late Payment Recovery)",
			current: PaymentStatusExpired,
			target:  PaymentStatusRequiresManualReview,
			wantErr: false,
		},
		{
			name:    "Succeeded to Refunded",
			current: PaymentStatusSucceeded,
			target:  PaymentStatusRefunded,
			wantErr: false,
		},
		{
			name:    "Same state transition (No-op)",
			current: PaymentStatusSucceeded,
			target:  PaymentStatusSucceeded,
			wantErr: false,
		},
		{
			name:    "Invalid: Refunded to Succeeded",
			current: PaymentStatusRefunded,
			target:  PaymentStatusSucceeded,
			wantErr: true,
		},
		{
			name:    "Invalid: Failed to Succeeded",
			current: PaymentStatusFailed,
			target:  PaymentStatusSucceeded,
			wantErr: true,
		},
		{
			name:    "Invalid: Expired to Succeeded directly",
			current: PaymentStatusExpired,
			target:  PaymentStatusSucceeded,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStateTransition(tt.current, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateStateTransition(%v, %v) error = %v, wantErr %v", tt.current, tt.target, err, tt.wantErr)
			}
		})
	}
}
