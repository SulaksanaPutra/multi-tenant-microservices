package domain

import "time"

type NotificationLog struct {
	ID             int
	UserID         string
	TenantID       string
	RecipientEmail string
	Subject        string
	Body           string
	Status         string
	CreatedAt      time.Time
}

type InboxMessage struct {
	EventID   string
	TenantID  string
	EventType string
	Payload   []byte
}
