package domain

type InboxMessage struct {
	EventID   string
	TenantID  string
	EventType string
	Payload   []byte
}
