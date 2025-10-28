package domain

import "time"

type Notification struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Recipient string    `json:"recipient"`
	Subject   string    `json:"subject"`
	Body      string    `json:"body"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
