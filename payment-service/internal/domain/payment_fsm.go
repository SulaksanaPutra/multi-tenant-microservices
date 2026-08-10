package domain

func ValidateStateTransition(current, target PaymentStatus) error {
	if current == target {
		return nil
	}

	validTransitions := map[PaymentStatus][]PaymentStatus{
		PaymentStatusPending: {
			PaymentStatusInstructionsReady,
			PaymentStatusFailed,
			PaymentStatusFailedAmountMismatch,
			PaymentStatusExpired,
		},
		PaymentStatusInstructionsReady: {
			PaymentStatusSucceeded,
			PaymentStatusFailed,
			PaymentStatusFailedAmountMismatch,
			PaymentStatusExpired,
			PaymentStatusRefunded,
		},
		PaymentStatusExpired: {
			PaymentStatusRequiresManualReview,
		},
		PaymentStatusSucceeded: {
			PaymentStatusRefunded,
		},
	}

	allowed, ok := validTransitions[current]
	if !ok {
		return ErrInvalidStatusTransition
	}

	for _, next := range allowed {
		if next == target {
			return nil
		}
	}

	return ErrInvalidStatusTransition
}
