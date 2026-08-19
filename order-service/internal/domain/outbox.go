package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

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

const (
	PrefixOutbox = "outbox_"
)

func GenerateOutboxID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixOutbox, raw)
}
