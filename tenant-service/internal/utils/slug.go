package utils

import (
	"regexp"
	"strings"
)

func SanitizeSchemaName(slug string) string {
	reg := regexp.MustCompile("[^a-zA-Z0-9_]+")
	cleanSlug := strings.Trim(reg.ReplaceAllString(strings.ToLower(slug), "_"), "_")
	return "tenant_" + cleanSlug
}
