package domain

import (
	"strings"
	"testing"
)

func TestGenerateDebtID(t *testing.T) {
	id := GenerateDebtID()
	if !strings.HasPrefix(id, PrefixDebt) {
		t.Errorf("Expected prefix '%s', got '%s'", PrefixDebt, id)
	}

	length := len(PrefixDebt) + 16
	if len(id) != length {
		t.Errorf("Expected length %d, got %d", length, len(id))
	}
}
