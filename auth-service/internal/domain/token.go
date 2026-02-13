package domain

import "time"

// RefreshToken represents a server-side opaque refresh token record.
// Only the SHA-256 hash of the raw token is persisted — the raw token itself
// is never stored and is only returned once to the caller at issuance time.
type RefreshToken struct {
	ID        string
	UserID    string
	TokenHash string    // SHA-256(raw token)
	ExpiresAt time.Time
	RevokedAt *time.Time // nil = active
	CreatedAt time.Time
}

// PasswordSetupToken represents a server-side password setup token record.
// Only the SHA-256 hash of the raw token is persisted.
type PasswordSetupToken struct {
	ID        string
	UserID    string
	TenantID  string
	Email     string
	TokenHash string    // SHA-256(raw token)
	ExpiresAt time.Time
	UsedAt    *time.Time // nil = unused
	CreatedAt time.Time
}

// JWTClaims are extracted from a verified RS256 JWT and injected into gin.Context
// by the JWT middleware so that downstream handlers can access identity without
// re-parsing the token.
type JWTClaims struct {
	UserID   string
	TenantID string
	Email    string
	JTI      string
}
