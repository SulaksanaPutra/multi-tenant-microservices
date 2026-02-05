package domain

import "time"

// Credential represents the stored authentication identity of a user.
// It is intentionally separate from the user profile (user-service domain).
// The tenant_id is stored here so token issuance does not require a cross-service call in Stage 1.
type Credential struct {
	UserID       string
	TenantID     string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
