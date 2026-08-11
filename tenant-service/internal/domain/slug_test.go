package domain_test

import (
	"testing"

	"tenant-service/internal/domain"
)

func TestSanitizeSlug(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Acme Corp", "acme-corp"},
		{"Acme  Corp!! ", "acme-corp"},
		{"---hello---world---", "hello-world"},
		{"123 TEST 456", "123-test-456"},
		{"", ""},
	}

	for _, tt := range tests {
		result := domain.SanitizeSlug(tt.input)
		if result != tt.expected {
			t.Errorf("SanitizeSlug(%q) = %q; expected %q", tt.input, result, tt.expected)
		}
	}
}
