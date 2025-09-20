package utils

import (
	"regexp"
	"strings"
)

// SanitizeSlug converts an arbitrary string (e.g. tenant name) into a safe, normalized slug string.
func SanitizeSlug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// SanitizeSchemaName converts a slug/tenant name into a safe postgres schema name prefixed with "tenant_".
func SanitizeSchemaName(slug string) string {
	reg := regexp.MustCompile("[^a-zA-Z0-9_]+")
	cleanSlug := strings.Trim(reg.ReplaceAllString(strings.ToLower(slug), "_"), "_")
	return "tenant_" + cleanSlug
}
