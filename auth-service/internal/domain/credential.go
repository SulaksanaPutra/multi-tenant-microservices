package domain

import "time"

// Credential represents the stored authentication identity of a user.
// It is intentionally separate from the user profile (user-service domain).
// It is a global identity row (one per email): tenant context is NOT stored
// here. Tenants are resolved at token issuance time from
// user_tenant_memberships via ListUserMemberships.
type Credential struct {
	UserID       string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
