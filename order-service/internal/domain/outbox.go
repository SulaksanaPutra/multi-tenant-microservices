package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// OutboxMessage represents a staged transactional outbox record.
type OutboxMessage struct {
	ID            string
	TenantID      string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	Status        string
	RetryCount    int
	CreatedAt     time.Time
}

// System entity ID prefixes.
const (
	PrefixOutbox = "outbox_"
)

// GenerateOutboxID produces a globally unique outbox record ID.
func GenerateOutboxID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixOutbox, raw)
}
