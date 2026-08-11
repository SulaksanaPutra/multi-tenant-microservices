package main

import (
	"go/parser"
	"go/token"
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

