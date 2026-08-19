package domain

import "time"

type RefreshToken struct {
	ID        string
	UserID    string
	TenantID  string
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

type PasswordSetupToken struct {
	ID        string
	UserID    string
	TenantID  string
	Email     string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

type JWTClaims struct {
	UserID      string
	TenantID    string
	Email       string
	JTI         string
	Permissions []string
	PermVersion int64
}

func (c *JWTClaims) HasPermission(perm string) bool {
	if c == nil {
		return false
	}
	for _, p := range c.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}
