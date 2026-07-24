package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type NotificationLog struct {
	ID          string
	UserID      string
	TenantID    string
	Description string
	Body        string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const (
	PrefixNotification = "ntf_"
)

// GenerateNotificationID produces a prefixed string identifier for a
// notification audit log row, e.g. "ntf_<32-hex-chars>".
func GenerateNotificationID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%s%x", PrefixNotification, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s%s", PrefixNotification, hex.EncodeToString(raw))
}
