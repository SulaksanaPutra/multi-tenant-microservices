package domain

import (
	"testing"
	"time"
)

func TestJWTClaims_HasPermission(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var claims *JWTClaims
		if claims.HasPermission("auth:read") {
			t.Error("expected false for nil claims receiver")
		}
	})

	t.Run("empty permissions slice", func(t *testing.T) {
		claims := &JWTClaims{
			Permissions: []string{},
		}
		if claims.HasPermission("auth:read") {
			t.Error("expected false for empty permissions")
		}
	})

	t.Run("matching permission present", func(t *testing.T) {
		claims := &JWTClaims{
			Permissions: []string{"tenant:read", "auth:read", "user:write"},
		}
		if !claims.HasPermission("auth:read") {
			t.Error("expected true when permission is present in slice")
		}
	})

	t.Run("permission not present", func(t *testing.T) {
		claims := &JWTClaims{
			Permissions: []string{"tenant:read", "user:write"},
		}
		if claims.HasPermission("auth:read") {
			t.Error("expected false when permission is missing from slice")
		}
	})

	t.Run("exact string matching", func(t *testing.T) {
		claims := &JWTClaims{
			Permissions: []string{"auth:read:all"},
		}
		if claims.HasPermission("auth:read") {
			t.Error("expected false for partial match")
		}
	})
}

func TestDomainTokens_Structs(t *testing.T) {
	now := time.Now()
	revoked := now.Add(time.Hour)

	rt := RefreshToken{
		ID:        "rt_123",
		UserID:    "usr_456",
		TokenHash: "sha256_hash",
		ExpiresAt: now.Add(24 * time.Hour),
		RevokedAt: &revoked,
		CreatedAt: now,
	}

	if rt.ID != "rt_123" || rt.RevokedAt == nil {
		t.Errorf("unexpected RefreshToken struct state: %+v", rt)
	}

	used := now.Add(30 * time.Minute)
	pst := PasswordSetupToken{
		ID:        "pst_123",
		UserID:    "usr_456",
		TenantID:  "tnt_789",
		Email:     "user@example.com",
		TokenHash: "setup_hash",
		ExpiresAt: now.Add(12 * time.Hour),
		UsedAt:    &used,
		CreatedAt: now,
	}

	if pst.TenantID != "tnt_789" || pst.UsedAt == nil {
		t.Errorf("unexpected PasswordSetupToken struct state: %+v", pst)
	}
}
