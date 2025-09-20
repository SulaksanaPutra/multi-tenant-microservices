package utils_test

import (
	"testing"

	"tenant-service/internal/utils"
)

func TestSanitizeSlug(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Acme Corp", "acme_corp"},
		{"  Foo-Bar!! 123 ", "foo_bar___123"},
		{"___special___", "special"},
		{"MyCompany_HQ", "mycompany_hq"},
	}

	for _, tt := range tests {
		if got := utils.SanitizeSlug(tt.input); got != tt.expected {
			t.Errorf("SanitizeSlug(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSanitizeSchemaName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"acme_corp", "tenant_acme_corp"},
		{"Foo-Bar", "tenant_foo_bar"},
	}

	for _, tt := range tests {
		if got := utils.SanitizeSchemaName(tt.input); got != tt.expected {
			t.Errorf("SanitizeSchemaName(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
