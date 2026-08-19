package domain

import "time"

type OutboxMessage struct {
	EventID     string
	RoutingKey  string
	Payload     []byte
	Status      string
	RetryCount  int
	LastError   string
	CreatedAt   time.Time
	PublishedAt *time.Time
}
