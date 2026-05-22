package migration

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"notification-service/migrations"
)

func sqlFiles(t *testing.T) []fs.DirEntry {
	t.Helper()
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("failed to read embedded migrations FS: %v", err)
	}
	var files []fs.DirEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		files = append(files, entry)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	return files
}

func TestEmbeddedMigrations_ContainsExpectedFiles(t *testing.T) {
	files := sqlFiles(t)

	var names []string
	for _, file := range files {
		names = append(names, file.Name())
	}

	for _, want := range []string{
		"00001_init_notification_schema.sql",
		"00002_backfill_notification_status.sql",
	} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected embedded migration %q, got %v", want, names)
		}
	}
}

func TestEmbeddedMigrations_HaveGooseUpAndDownAnnotations(t *testing.T) {
	for _, file := range sqlFiles(t) {
		data, err := fs.ReadFile(migrations.FS, file.Name())
		if err != nil {
			t.Fatalf("failed to read migration %s: %v", file.Name(), err)
		}
		content := string(data)
		if !strings.Contains(content, "-- +goose Up") {
			t.Errorf("migration %s must contain '-- +goose Up' annotation", file.Name())
		}
		if !strings.Contains(content, "-- +goose Down") {
			t.Errorf("migration %s must contain '-- +goose Down' annotation", file.Name())
		}
	}
}

func TestEmbeddedMigrations_VersionPrefixesAreUniqueAndOrdered(t *testing.T) {
	seen := make(map[string]string)
	for _, file := range sqlFiles(t) {
		parts := strings.SplitN(file.Name(), "_", 2)
		if len(parts) != 2 {
			t.Errorf("migration %s must use the <version>_<description>.sql naming convention", file.Name())
			continue
		}
		version := parts[0]
		if prev, ok := seen[version]; ok {
			t.Errorf("duplicate migration version %s used by both %s and %s", version, prev, file.Name())
		}
		seen[version] = file.Name()
	}
	if len(seen) == 0 {
		t.Error("expected at least one embedded migration")
	}
}
