package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type Order struct {
	ID         string
	TenantID   string
	CustomerID string
	Status     string
	Amount     float64
}

const (
	PrefixOrder = "ord_"
)

func GenerateOrderID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixOrder, raw[:16])
}

