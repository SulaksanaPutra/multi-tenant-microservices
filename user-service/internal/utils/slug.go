package utils

import (
	"regexp"
	"strings"
)

func SanitizeSlug(slug string) string {
	reg := regexp.MustCompile("[^a-zA-Z0-9_]+")
	clean := reg.ReplaceAllString(strings.ToLower(slug), "_")
	return strings.Trim(clean, "_")
}
