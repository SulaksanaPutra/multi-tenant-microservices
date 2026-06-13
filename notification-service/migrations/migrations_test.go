package migrations

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var migrationNameRe = regexp.MustCompile(`^\d+_[a-z0-9_]+\.sql$`)

func TestEmbeddedMigrations_Structural(t *testing.T) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		t.Fatalf("failed to read embedded migration dir: %v", err)
	}

	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("expected at least one embedded SQL migration")
	}

	versions := make([]int, 0, len(names))
	for _, name := range names {
		if !migrationNameRe.MatchString(name) {
			t.Errorf("migration %q does not match NNNNN_description.sql", name)
		}
		parts := strings.SplitN(name, "_", 2)
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			t.Errorf("migration %q has non-numeric version prefix", name)
			continue
		}
		versions = append(versions, v)

		data, err := FS.ReadFile(name)
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		content := string(data)
		if !strings.Contains(content, "-- +goose Up") {
			t.Errorf("migration %q missing `-- +goose Up` annotation", name)
		}
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "CREATE TABLE") && !strings.Contains(trimmed, "IF NOT EXISTS") {
				t.Errorf("migration %q has non-idempotent DDL: %s", name, trimmed)
			}
		}
	}

	sorted := append([]int(nil), versions...)
	sort.Ints(sorted)
	for i := range versions {
		if versions[i] != sorted[i] {
			t.Errorf("migration versions out of order; got %v want %v", versions, sorted)
		}
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			t.Errorf("duplicate migration version %d", sorted[i])
		}
	}
}
