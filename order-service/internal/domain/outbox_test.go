package domain_test

import (
	"strings"
	"testing"

	"order-service/internal/domain"
)

func TestGenerateOutboxID(t *testing.T) {
	id := domain.GenerateOutboxID()
	if !strings.HasPrefix(id, domain.PrefixOutbox) {
		t.Errorf("Expected prefix '%s', got '%s'", domain.PrefixOutbox, id)
	}
	if len(id) <= len(domain.PrefixOutbox) {
		t.Errorf("Expected generated ID to contain unique body")
	}
}
