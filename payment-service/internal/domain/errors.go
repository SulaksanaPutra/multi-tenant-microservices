package domain

import "errors"

var (
	ErrPaymentNotFound          = errors.New("payment not found")
	ErrDebtNotFound             = errors.New("payable debt not found")
	ErrDebtAlreadyPaid          = errors.New("payable debt already paid")
	ErrInvalidStatusTransition  = errors.New("invalid payment status transition")
	ErrPaymentAmountMismatch    = errors.New("payment webhook amount or currency mismatch")
	ErrInvalidWebhookSignature  = errors.New("invalid webhook signature")
	ErrNoAvailableProvider      = errors.New("no available payment provider in fallback chain")
	ErrProviderTransientFailure = errors.New("payment provider transient failure")
	ErrDuplicateEvent           = errors.New("duplicate event already processed in inbox")
	ErrInvalidCiphertext        = errors.New("invalid ciphertext or decryption failure")
	ErrEmptyMasterKey           = errors.New("master key is empty")
	ErrCircuitOpen              = errors.New("circuit breaker is open")
	ErrInvalidPaymentMethod     = errors.New("invalid or unsupported payment method")
)
