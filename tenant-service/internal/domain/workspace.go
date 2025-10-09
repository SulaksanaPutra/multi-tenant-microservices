package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Plan represents the tenant hosting/pricing plan tier.
type Plan string

const (
	PlanShared    Plan = "shared"
	PlanDedicated Plan = "dedicated"
)

// IsValid checks whether the plan is an allowed plan type.
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

// System entity ID prefixes.
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
