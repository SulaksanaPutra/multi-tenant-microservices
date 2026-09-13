package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)



type PaymentStatus string

const (
	PaymentStatusPending               PaymentStatus = "PENDING"
	PaymentStatusInstructionsReady     PaymentStatus = "PAYMENT_INSTRUCTIONS_READY"
	PaymentStatusSucceeded             PaymentStatus = "SUCCEEDED"
	PaymentStatusFailed                PaymentStatus = "FAILED"
	PaymentStatusCancelled             PaymentStatus = "CANCELLED"
	PaymentStatusFailedAmountMismatch  PaymentStatus = "FAILED_AMOUNT_MISMATCH"
	PaymentStatusExpired               PaymentStatus = "EXPIRED"
	PaymentStatusRequiresManualReview PaymentStatus = "REQUIRES_MANUAL_REVIEW"
	PaymentStatusRefunded              PaymentStatus = "REFUNDED"
)

type AttemptStatus string

const (
	AttemptStatusPending   AttemptStatus = "PENDING"
	AttemptStatusTimedOut  AttemptStatus = "TIMED_OUT"
	AttemptStatusSuccess   AttemptStatus = "SUCCESS"
	AttemptStatusCancelled AttemptStatus = "CANCELLED"
	AttemptStatusFailed    AttemptStatus = "FAILED"
)

type InstructionType string

const (
	InstructionRedirectURL    InstructionType = "REDIRECT_URL"
	InstructionVirtualAccount InstructionType = "VIRTUAL_ACCOUNT"
	InstructionQRIS            InstructionType = "QRIS"
	InstructionDeepLink        InstructionType = "DEEP_LINK"
)

type PaymentInstructions struct {
	Type         InstructionType
	RedirectURL  string
	VANumber     string
	BankCode     string
	QRCodeString string
	DeepLink     string
	ExpiresAt    time.Time
}



type Payment struct {
	ID                string
	DebtID            string
	TenantID          string
	OrderID           string
	Amount            float64
	Currency          string
	Status            PaymentStatus
	Provider          ProviderType
	PaymentMethod     string
	ExternalID        string
	Instructions      PaymentInstructions
	RawWebhookPayload map[string]any
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type PaymentAttempt struct {
	ID                string
	PaymentID         string
	TenantID          string
	Provider          ProviderType
	ExternalSessionID string
	Status            AttemptStatus
	ErrorMessage      string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

const (
	PrefixPayment = "pay_"
	PrefixAttempt = "att_"
)

func GeneratePaymentID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixPayment, raw[:16])
}

func GenerateAttemptID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixAttempt, raw[:16])
}
