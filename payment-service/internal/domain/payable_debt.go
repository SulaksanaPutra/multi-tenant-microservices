package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DebtStatus string

const (
	DebtStatusUnpaid        DebtStatus = "UNPAID"
	DebtStatusPartiallyPaid DebtStatus = "PARTIALLY_PAID"
	DebtStatusPaid          DebtStatus = "PAID"
	DebtStatusExpired       DebtStatus = "EXPIRED"
	DebtStatusCancelled     DebtStatus = "CANCELLED"
)

type PayableDebt struct {
	ID          string
	TenantID    string
	OrderID     string
	TotalAmount float64
	PaidAmount  float64
	Currency    string
	Status      DebtStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const (
	PrefixDebt = "debt_"
)

func GenerateDebtID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixDebt, raw[:16])
}
