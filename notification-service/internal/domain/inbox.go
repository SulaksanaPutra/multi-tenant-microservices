package domain

// InboxMessage is an inbound event claimed by the notification-service
// transactional inbox guard. The inbox table serves as a dedup/barrier store:
// event_id is the primary key, so claiming an event is idempotent.
type InboxMessage struct {
	EventID   string
	TenantID  string
	EventType string
	Payload   []byte
}
