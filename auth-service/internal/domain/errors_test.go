package domain_test

import (
	"testing"

	"auth-service/internal/domain"
)

func TestDomainSentinelErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"ErrInvalidCredentials", domain.ErrInvalidCredentials, "auth service: invalid email or password"},
		{"ErrCredentialNotFound", domain.ErrCredentialNotFound, "auth service: credential not found"},
		{"ErrTokenExpired", domain.ErrTokenExpired, "auth service: token has expired"},
		{"ErrTokenRevoked", domain.ErrTokenRevoked, "auth service: token has been revoked"},
		{"ErrTokenAlreadyUsed", domain.ErrTokenAlreadyUsed, "auth service: setup token has already been used"},
		{"ErrTokenNotFound", domain.ErrTokenNotFound, "auth service: token not found"},
		{"ErrEmailRequired", domain.ErrEmailRequired, "auth service: email is required"},
		{"ErrPasswordRequired", domain.ErrPasswordRequired, "auth service: password is required"},
		{"ErrUserIDRequired", domain.ErrUserIDRequired, "auth service: user_id is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatalf("expected %s to be non-nil", tt.name)
			}
			if tt.err.Error() != tt.expected {
				t.Errorf("expected error message '%s', got '%s'", tt.expected, tt.err.Error())
			}
		})
	}
}
