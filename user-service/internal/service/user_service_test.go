package service

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"user-service/internal/domain"
	"user-service/internal/repository"
)

type mockUserRepository struct {
	createUserFunc func(ctx context.Context, input repository.CreateUserInput) error
}

func (m *mockUserRepository) CreateUser(ctx context.Context, input repository.CreateUserInput) error {
	if m.createUserFunc != nil {
		return m.createUserFunc(ctx, input)
	}
	return nil
}

type mockOutboxRepository struct {
	createOutboxMessageFunc func(ctx context.Context, input repository.CreateOutboxMessageInput) error
}

func (m *mockOutboxRepository) CreateOutboxMessage(ctx context.Context, input repository.CreateOutboxMessageInput) error {
	if m.createOutboxMessageFunc != nil {
		return m.createOutboxMessageFunc(ctx, input)
	}
	return nil
}

func TestUserService_CreateUserFromWorkspace_Validation(t *testing.T) {
	svc := NewUserService(&mockUserRepository{}, &mockOutboxRepository{})

	tests := []struct {
		name    string
		input   CreateUserFromWorkspaceInput
		wantErr error
	}{
		{
			name: "missing tenant_id",
			input: CreateUserFromWorkspaceInput{
				TenantID:   "",
				OwnerEmail: "owner@company.com",
				OwnerName:  "Alice",
			},
			wantErr: ErrTenantIDRequired,
		},
		{
			name: "missing owner_email",
			input: CreateUserFromWorkspaceInput{
				TenantID:   "tenant-123",
				OwnerEmail: "",
				OwnerName:  "Alice",
			},
			wantErr: ErrEmailRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.CreateUserFromWorkspace(context.Background(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestUserService_CreateUserFromWorkspace_Success(t *testing.T) {
	var createdUser repository.CreateUserInput
	var createdOutbox repository.CreateOutboxMessageInput

	userRepo := &mockUserRepository{
		createUserFunc: func(ctx context.Context, input repository.CreateUserInput) error {
			createdUser = input
			return nil
		},
	}

	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, input repository.CreateOutboxMessageInput) error {
			createdOutbox = input
			return nil
		},
	}

	svc := NewUserService(userRepo, outboxRepo)

	input := CreateUserFromWorkspaceInput{
		EventID:    "evt-123",
		TenantID:   "tenant-456",
		OwnerEmail: "owner@company.com",
		OwnerName:  "Alice Smith",
	}

	err := svc.CreateUserFromWorkspace(context.Background(), input)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if createdUser.Email != "owner@company.com" || createdUser.Name != "Alice Smith" {
		t.Errorf("unexpected user record created: %+v", createdUser)
	}

	if createdOutbox.AggregateID != createdUser.ID || createdOutbox.EventType != domain.RoutingKeyUserCreated {
		t.Errorf("unexpected outbox message created: %+v", createdOutbox)
	}
}

func TestUserService_CreateUserFromWorkspace_NilOutboxRepo(t *testing.T) {
	var createdUser repository.CreateUserInput
	userRepo := &mockUserRepository{
		createUserFunc: func(ctx context.Context, input repository.CreateUserInput) error {
			createdUser = input
			return nil
		},
	}

	// outboxRepository is nil
	svc := NewUserService(userRepo, nil)

	input := CreateUserFromWorkspaceInput{
		EventID:    "evt-123",
		TenantID:   "tenant-456",
		OwnerEmail: "owner@company.com",
		OwnerName:  "Alice Smith",
	}

	err := svc.CreateUserFromWorkspace(context.Background(), input)
	if err != nil {
		t.Fatalf("expected no error with nil outbox repo, got: %v", err)
	}

	if createdUser.Email != "owner@company.com" {
		t.Errorf("unexpected user record created: %+v", createdUser)
	}
}

func TestUserService_CreateUserFromWorkspace_UserRepoError(t *testing.T) {
	expectedErr := errors.New("db error")
	userRepo := &mockUserRepository{
		createUserFunc: func(ctx context.Context, input repository.CreateUserInput) error {
			return expectedErr
		},
	}

	svc := NewUserService(userRepo, nil)

	input := CreateUserFromWorkspaceInput{
		TenantID:   "tenant-123",
		OwnerEmail: "test@example.com",
	}

	err := svc.CreateUserFromWorkspace(context.Background(), input)
	if err == nil || !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping '%v', got '%v'", expectedErr, err)
	}
}

func TestUserService_CreateUserFromWorkspace_OutboxRepoError(t *testing.T) {
	userRepo := &mockUserRepository{}
	expectedErr := errors.New("outbox write failure")
	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, input repository.CreateOutboxMessageInput) error {
			return expectedErr
		},
	}

	svc := NewUserService(userRepo, outboxRepo)

	input := CreateUserFromWorkspaceInput{
		TenantID:   "tenant-123",
		OwnerEmail: "test@example.com",
	}

	err := svc.CreateUserFromWorkspace(context.Background(), input)
	if err == nil || !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping '%v', got '%v'", expectedErr, err)
	}
}

func TestUserService_ErrorContractInvariants(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "user_service.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse user_service.go AST: %v", err)
	}

	var errorVarsFound int
	for _, decl := range node.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if !strings.HasPrefix(name.Name, "Err") {
					t.Errorf("sentinel error variable '%s' must start with prefix 'Err'", name.Name)
				}
				errorVarsFound++
				if i < len(valueSpec.Values) {
					if call, ok := valueSpec.Values[i].(*ast.CallExpr); ok {
						if len(call.Args) > 0 {
							if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								errStr := strings.Trim(lit.Value, `"`)
								if !strings.HasPrefix(errStr, "user service:") {
									t.Errorf("sentinel error '%s' message '%s' must start with prefix 'user service:'", name.Name, errStr)
								}
							}
						}
					}
				}
			}
		}
	}
	if errorVarsFound == 0 {
		t.Error("expected at least one sentinel error declaration in user_service.go")
	}
}
