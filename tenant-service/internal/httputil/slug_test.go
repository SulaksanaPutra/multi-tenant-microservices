package httputil

import (
	"testing"
)

func TestSanitizeSlug(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard tenant name",
			input:    "Acme Corporation",
			expected: "acme-corporation",
		},
		{
			name:     "special characters and spaces",
			input:    "  Acme & Co. (2026)!  ",
			expected: "acme-co-2026",
		},
		{
			name:     "consecutive non-alphanumeric characters",
			input:    "Foo---Bar!!!Baz",
			expected: "foo-bar-baz",
		},
		{
			name:     "already sanitized slug",
			input:    "my-tenant-123",
			expected: "my-tenant-123",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeSlug(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeSlug(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}
