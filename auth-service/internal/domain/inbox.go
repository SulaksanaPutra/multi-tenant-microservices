package domain

// InboxMessage is the canonical event-inbox shape shared across the platform.
type InboxMessage struct {
	EventID   string
	TenantID  string
	EventType string
	Payload   []byte
}
