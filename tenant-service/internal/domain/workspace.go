package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type Plan string

const (
	PlanShared    Plan = "shared"
	PlanDedicated Plan = "dedicated"
)

func (p Plan) IsValid() bool {
	switch p {
	case PlanShared, PlanDedicated:
		return true
	default:
		return false
	}
}

func (p Plan) String() string {
	return string(p)
}

const (
	PrefixTenant = "tnt_"
	PrefixOutbox = "outbox_"
)

func GenerateTenantID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixTenant, raw[:16])
}

func GenerateOutboxID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixOutbox, raw)
}
