package httputil

import (
	"regexp"
	"strings"
)

var (
	nonAlphanumericRegex = regexp.MustCompile(`[^a-z0-9]+`)
	schemaCleanRegex    = regexp.MustCompile(`[^a-z0-9_]`)
)

// SanitizeSlug converts an arbitrary string (e.g. tenant name) into a safe, normalized slug string.
func SanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlphanumericRegex.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// SanitizeSchemaName converts a slug/tenant name into a safe postgres schema name prefixed with "tenant_".
func SanitizeSchemaName(slug string) string {
	s := strings.ToLower(strings.TrimSpace(slug))
	s = strings.ReplaceAll(s, "-", "_")
	s = schemaCleanRegex.ReplaceAllString(s, "")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "default"
	}
	return "tenant_" + s
}
