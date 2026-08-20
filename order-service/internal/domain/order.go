package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type OrderStatus string

type Order struct {
	ID         string
	TenantID   string
	CustomerID string
	Status     OrderStatus
	Quantity   int
	Price      float64
	Amount     float64
	Currency   string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const (
	PrefixOrder              = "ord_"
	StatusPending   OrderStatus = "PENDING"
	StatusCompleted OrderStatus = "COMPLETED"
	StatusCancelled OrderStatus = "CANCELLED"
)

func GenerateOrderID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixOrder, raw[:16])
}

