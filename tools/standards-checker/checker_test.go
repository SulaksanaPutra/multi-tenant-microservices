package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckFile_Rule2_1_ConstructorConcretePointer(t *testing.T) {
	src := `package service

type UserService struct{}

func NewUserService() UserService {
	return UserService{}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/service/user_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for returning non-pointer struct in constructor, got %d", len(violations))
	}
	if violations[0].ID != "constructor-returns-concrete" {
		t.Errorf("Expected violation ID 'constructor-returns-concrete', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule3_1_GetReturnsCollection(t *testing.T) {
	src := `package repository

type OrderRepository struct{}

func (r *OrderRepository) GetOrders() ([]string, error) {
	return nil, nil
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "order_repository.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "order-service", "internal/repository/order_repository.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for Get* returning slice, got %d", len(violations))
	}
	if violations[0].ID != "get-returns-collection" {
		t.Errorf("Expected violation ID 'get-returns-collection', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule4_4_SQLInService(t *testing.T) {
	src := `package service

import "database/sql"

type UserService struct{
	db *sql.DB
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/service/user_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for database/sql import in service layer, got %d", len(violations))
	}
	if violations[0].ID != "sql-in-service" {
		t.Errorf("Expected violation ID 'sql-in-service', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule6_1_JSONTagInService(t *testing.T) {
	src := `package service

type CreateUserDTO struct {
	Name string ` + "`json:\"name\"`" + `
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/service/user_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for json tag in service layer, got %d", len(violations))
	}
	if violations[0].ID != "json-tag-in-service" {
		t.Errorf("Expected violation ID 'json-tag-in-service', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule4_1_SentinelOutsideDomain(t *testing.T) {
	src := `package service

import "errors"

var ErrUserNotFound = errors.New("user not found")
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_service.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/service/user_service.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for sentinel error outside domain, got %d", len(violations))
	}
	if violations[0].ID != "sentinel-outside-domain" {
		t.Errorf("Expected violation ID 'sentinel-outside-domain', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule2_2_StructConcreteDependency(t *testing.T) {
	src := `package handler

import "user-service/internal/service"

type UserHandler struct {
	userService *service.UserService
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "user_handler.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "user-service", "internal/handler/user_handler.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for struct concrete dependency, got %d", len(violations))
	}
	if violations[0].ID != "struct-concrete-dependency" {
		t.Errorf("Expected violation ID 'struct-concrete-dependency', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule7_1_HardcodedCryptoFallback(t *testing.T) {
	src := `package main

const pubKey = "-----BEGIN PUBLIC KEY-----\nsomekey\n-----END PUBLIC KEY-----"
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "cmd/main.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for hardcoded crypto fallback, got %d", len(violations))
	}
	if violations[0].ID != "hardcoded-crypto-fallback" {
		t.Errorf("Expected violation ID 'hardcoded-crypto-fallback', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule1_3_EnvNamingConvention(t *testing.T) {
	src := `package main

import "os"

func main() {
	port := os.Getenv("PAYMENT_SERVICE_PORT")
	_ = port
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "payment-service/cmd/main.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for env naming convention, got %d", len(violations))
	}
	if violations[0].ID != "env-naming-convention" {
		t.Errorf("Expected violation ID 'env-naming-convention', got '%s'", violations[0].ID)
	}
}

func TestCheckFile_Rule3_4_AbbrevMgr(t *testing.T) {
	src := `package main

type TestStruct struct {
	txMgr string
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		t.Fatalf("Failed to parse Go source: %v", err)
	}

	violations := checkFile(fset, file, "payment-service", "cmd/main.go", false, false)
	if len(violations) != 1 {
		t.Fatalf("Expected 1 violation for abbrev-mgr, got %d", len(violations))
	}
	if violations[0].ID != "abbrev-mgr" {
		t.Errorf("Expected violation ID 'abbrev-mgr', got '%s'", violations[0].ID)
	}
}

func TestCheckDockerStandards_Rule9_HardcodedPort(t *testing.T) {
	tmpDir := t.TempDir()
	// write .dockerignore
	_ = os.WriteFile(filepath.Join(tmpDir, ".dockerignore"), []byte(".env\n.git\n"), 0644)
	// write Dockerfile with cache mounts
	dfContent := `FROM golang:alpine AS builder
RUN --mount=type=cache,target=/go/pkg/mod go mod download
RUN --mount=type=cache,target=/root/.cache/go-build go build -o app
`
	_ = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dfContent), 0644)
	// write docker-compose with hardcoded port
	composeContent := `services:
  test-service:
    environment:
      PORT: 8085
      DB_HOST: ${AUTH_DB_HOST:-postgres}
`
	_ = os.WriteFile(filepath.Join(tmpDir, "docker-compose.yml"), []byte(composeContent), 0644)

	violations := checkDockerStandards(tmpDir, "auth-service", tmpDir)
	hasHardcodedPort := false
	for _, v := range violations {
		if v.ID == "hardcoded-compose-port" {
			hasHardcodedPort = true
			break
		}
	}
	if !hasHardcodedPort {
		t.Errorf("Expected violation 'hardcoded-compose-port', got violations: %+v", violations)
	}
}

func TestCheckDockerStandards_Rule9_MissingDockerignoreEnv(t *testing.T) {
	tmpDir := t.TempDir()
	// write .dockerignore without .env
	_ = os.WriteFile(filepath.Join(tmpDir, ".dockerignore"), []byte(".git\n.idea\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte("--mount=type=cache,target=/go/pkg/mod\n--mount=type=cache,target=/root/.cache/go-build\n"), 0644)

	violations := checkDockerStandards(tmpDir, "infra-provisioner", tmpDir)
	hasMissingEnv := false
	for _, v := range violations {
		if v.ID == "dockerignore-missing-env" {
			hasMissingEnv = true
			break
		}
	}
	if !hasMissingEnv {
		t.Errorf("Expected violation 'dockerignore-missing-env', got violations: %+v", violations)
	}
}

func TestCheckDockerStandards_Rule9_ArchetypeCStandaloneComposeProhibited(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, ".dockerignore"), []byte(".env\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte("--mount=type=cache,target=/go/pkg/mod\n--mount=type=cache,target=/root/.cache/go-build\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "docker-compose.yml"), []byte("services:\n  infra:\n"), 0644)

	violations := checkDockerStandards(tmpDir, "infra-provisioner", tmpDir)
	hasProhibitedCompose := false
	for _, v := range violations {
		if v.ID == "archetype-c-standalone-compose" {
			hasProhibitedCompose = true
			break
		}
	}
	if !hasProhibitedCompose {
		t.Errorf("Expected violation 'archetype-c-standalone-compose', got violations: %+v", violations)
	}
}
