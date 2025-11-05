package domain

import "time"

type OutboxMessage struct {
	ID            string
	TenantID      *string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	Status        string
	RetryCount    int
	LastError     *string
	NextRetryAt   *time.Time
	ClaimedAt     *time.Time
	CreatedAt     time.Time
	ProcessedAt   *time.Time
}
