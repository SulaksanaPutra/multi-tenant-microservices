package domain_test

import (
	"strings"
	"testing"

	"user-service/internal/domain"
)

func TestGenerateUserID(t *testing.T) {
	id1 := domain.GenerateUserID()
	id2 := domain.GenerateUserID()

	if !strings.HasPrefix(id1, domain.PrefixUser) {
		t.Errorf("expected prefix '%s', got '%s'", domain.PrefixUser, id1)
	}

	if len(id1) != len(domain.PrefixUser)+16 {
		t.Errorf("expected length %d, got %d", len(domain.PrefixUser)+16, len(id1))
	}

	if id1 == id2 {
		t.Errorf("expected unique IDs, got duplicate '%s'", id1)
	}
}

func TestUser_Struct(t *testing.T) {
	user := domain.User{
		ID:    "usr_1234567890123456",
		Email: "user@test.com",
		Name:  "Test User",
	}

	if user.ID != "usr_1234567890123456" || user.Email != "user@test.com" || user.Name != "Test User" {
		t.Errorf("unexpected User struct values: %+v", user)
	}
}
