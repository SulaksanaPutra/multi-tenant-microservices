package domain_test

import (
	"testing"
	"time"

	"auth-service/internal/domain"
)

func TestCredentialStruct(t *testing.T) {
	now := time.Now()
	cred := domain.Credential{
		UserID:       "usr_123",
		TenantID:     "tnt_456",
		Email:        "user@example.com",
		PasswordHash: "hashed_pwd",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if cred.UserID != "usr_123" || cred.TenantID != "tnt_456" || cred.Email != "user@example.com" {
		t.Errorf("unexpected Credential values: %+v", cred)
	}
}

func TestTokenStructs(t *testing.T) {
	now := time.Now()
	usedAt := now.Add(time.Minute)
	revokedAt := now.Add(time.Minute)

	rt := domain.RefreshToken{
		ID:        "rt_1",
		UserID:    "usr_123",
		TokenHash: "hash_1",
		ExpiresAt: now.Add(24 * time.Hour),
		RevokedAt: &revokedAt,
		CreatedAt: now,
	}

	if rt.ID != "rt_1" || rt.UserID != "usr_123" || rt.RevokedAt == nil {
		t.Errorf("unexpected RefreshToken values: %+v", rt)
	}

	pst := domain.PasswordSetupToken{
		ID:        "pst_1",
		UserID:    "usr_123",
		TenantID:  "tnt_456",
		Email:     "user@example.com",
		TokenHash: "hash_setup",
		ExpiresAt: now.Add(24 * time.Hour),
		UsedAt:    &usedAt,
		CreatedAt: now,
	}

	if pst.ID != "pst_1" || pst.UsedAt == nil {
		t.Errorf("unexpected PasswordSetupToken values: %+v", pst)
	}

	claims := domain.JWTClaims{
		UserID:   "usr_123",
		TenantID: "tnt_456",
		Email:    "user@example.com",
		JTI:      "jti_789",
	}

	if claims.UserID != "usr_123" || claims.JTI != "jti_789" {
		t.Errorf("unexpected JWTClaims values: %+v", claims)
	}
}
