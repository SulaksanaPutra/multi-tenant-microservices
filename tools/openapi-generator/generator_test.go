package main

import (
	"testing"
)

func TestFindRepoRoot(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("Failed to find repo root: %v", err)
	}
	if root == "" {
		t.Fatal("Repo root returned empty string")
	}
}

func TestGenerateServiceSpec(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("Failed to find repo root: %v", err)
	}

	svc := ServiceMeta{Name: "auth-service", Title: "Auth Service", Port: 8085}
	spec := generateServiceSpec(root, svc, "")

	if spec.Info.Title != "Auth Service API" {
		t.Errorf("Expected title 'Auth Service API', got '%s'", spec.Info.Title)
	}
	if len(spec.Paths) == 0 {
		t.Errorf("Expected paths to be extracted for auth-service, got 0")
	}
}
