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

func TestSanitizeSchemaName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard slug",
			input:    "acme-corporation",
			expected: "tenant_acme_corporation",
		},
		{
			name:     "slug with special symbols",
			input:    "acme-co@2026!",
			expected: "tenant_acme_co2026",
		},
		{
			name:     "empty input falls back to default",
			input:    "",
			expected: "tenant_default",
		},
		{
			name:     "dashes and underscores mix",
			input:    "foo_bar-baz",
			expected: "tenant_foo_bar_baz",
		},
		{
			name:     "dashes only input falls back to default",
			input:    "---",
			expected: "tenant_default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeSchemaName(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeSchemaName(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}
