package domain

import (
	"strings"
	"testing"
)

func TestGenerateUserID(t *testing.T) {
	id1 := GenerateUserID()
	id2 := GenerateUserID()

	if !strings.HasPrefix(id1, PrefixUser) {
		t.Errorf("expected prefix '%s', got '%s'", PrefixUser, id1)
	}

	if len(id1) != len(PrefixUser)+16 {
		t.Errorf("expected length %d, got %d", len(PrefixUser)+16, len(id1))
	}

	if id1 == id2 {
		t.Errorf("expected unique IDs, got duplicate '%s'", id1)
	}
}
