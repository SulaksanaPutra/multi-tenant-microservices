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
	createUserFunc     func(ctx context.Context, input repository.CreateUserInput) error
	getUserByEmailFunc func(ctx context.Context, email string) (*domain.User, error)
	getUserByIDFunc    func(ctx context.Context, userID string) (*domain.User, error)
	updateUserFunc     func(ctx context.Context, input repository.UpdateUserInput) error
	listUsersFunc      func(ctx context.Context, tenantID string) ([]domain.User, error)
	addMembershipFunc  func(ctx context.Context, userID, tenantID string) error
}

func (m *mockUserRepository) CreateUser(ctx context.Context, input repository.CreateUserInput) error {
	if m.createUserFunc != nil {
		return m.createUserFunc(ctx, input)
	}
	return nil
}

func (m *mockUserRepository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	if m.getUserByEmailFunc != nil {
		return m.getUserByEmailFunc(ctx, email)
	}
	return nil, domain.ErrNotFound
}

func (m *mockUserRepository) GetUserByID(ctx context.Context, userID string) (*domain.User, error) {
	if m.getUserByIDFunc != nil {
		return m.getUserByIDFunc(ctx, userID)
	}
	return &domain.User{ID: userID}, nil
}

func (m *mockUserRepository) UpdateUser(ctx context.Context, input repository.UpdateUserInput) error {
	if m.updateUserFunc != nil {
		return m.updateUserFunc(ctx, input)
	}
	return nil
}

func (m *mockUserRepository) ListUsers(ctx context.Context, tenantID string) ([]domain.User, error) {
	if m.listUsersFunc != nil {
		return m.listUsersFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockUserRepository) AddUserTenantMembership(ctx context.Context, userID, tenantID string) error {
	if m.addMembershipFunc != nil {
		return m.addMembershipFunc(ctx, userID, tenantID)
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
	userService := NewUserService(&mockUserRepository{}, &mockOutboxRepository{})

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
			wantErr: domain.ErrTenantIDRequired,
		},
		{
			name: "missing owner_email",
			input: CreateUserFromWorkspaceInput{
				TenantID:   "tenant-123",
				OwnerEmail: "",
				OwnerName:  "Alice",
			},
			wantErr: domain.ErrEmailRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := userService.CreateUserFromWorkspace(context.Background(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestUserService_CreateUserFromWorkspace_Success(t *testing.T) {
	var createdUser repository.CreateUserInput
	var createdOutbox repository.CreateOutboxMessageInput
	var membershipAdded string

	userRepository := &mockUserRepository{
		createUserFunc: func(ctx context.Context, input repository.CreateUserInput) error {
			createdUser = input
			return nil
		},
		addMembershipFunc: func(ctx context.Context, userID, tenantID string) error {
			membershipAdded = tenantID
			return nil
		},
	}

	outboxRepository := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, input repository.CreateOutboxMessageInput) error {
			createdOutbox = input
			return nil
		},
	}

	userService := NewUserService(userRepository, outboxRepository)

	input := CreateUserFromWorkspaceInput{
		EventID:    "evt-123",
		TenantID:   "tenant-456",
		OwnerEmail: "owner@company.com",
		OwnerName:  "Alice Smith",
	}

	err := userService.CreateUserFromWorkspace(context.Background(), input)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if createdUser.Email != "owner@company.com" || createdUser.Name != "Alice Smith" {
		t.Errorf("unexpected user record created: %+v", createdUser)
	}

	if createdOutbox.AggregateID != createdUser.ID || createdOutbox.EventType != domain.RoutingKeyUserCreated {
		t.Errorf("unexpected outbox message created: %+v", createdOutbox)
	}

	if membershipAdded != "tenant-456" {
		t.Errorf("expected tenant membership to be added for tenant-456, got %q", membershipAdded)
	}
}

func TestUserService_CreateUserFromWorkspace_NilOutboxRepository(t *testing.T) {
	var createdUser repository.CreateUserInput
	userRepository := &mockUserRepository{
		createUserFunc: func(ctx context.Context, input repository.CreateUserInput) error {
			createdUser = input
			return nil
		},
	}

	userService := NewUserService(userRepository, nil)

	input := CreateUserFromWorkspaceInput{
		EventID:    "evt-123",
		TenantID:   "tenant-456",
		OwnerEmail: "owner@company.com",
		OwnerName:  "Alice Smith",
	}

	err := userService.CreateUserFromWorkspace(context.Background(), input)
	if err != nil {
		t.Fatalf("expected no error with nil outbox repo, got: %v", err)
	}

	if createdUser.Email != "owner@company.com" {
		t.Errorf("unexpected user record created: %+v", createdUser)
	}
}

func TestUserService_CreateUserFromWorkspace_UserRepoError(t *testing.T) {
	expectedErr := errors.New("db error")
	userRepository := &mockUserRepository{
		createUserFunc: func(ctx context.Context, input repository.CreateUserInput) error {
			return expectedErr
		},
	}

	userService := NewUserService(userRepository, nil)

	input := CreateUserFromWorkspaceInput{
		TenantID:   "tenant-123",
		OwnerEmail: "test@example.com",
	}

	err := userService.CreateUserFromWorkspace(context.Background(), input)
	if err == nil || !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping '%v', got '%v'", expectedErr, err)
	}
}

func TestUserService_CreateUserFromWorkspace_OutboxRepoError(t *testing.T) {
	userRepository := &mockUserRepository{}
	expectedErr := errors.New("outbox write failure")
	outboxRepository := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, input repository.CreateOutboxMessageInput) error {
			return expectedErr
		},
	}

	userService := NewUserService(userRepository, outboxRepository)

	input := CreateUserFromWorkspaceInput{
		TenantID:   "tenant-123",
		OwnerEmail: "test@example.com",
	}

	err := userService.CreateUserFromWorkspace(context.Background(), input)
	if err == nil || !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping '%v', got '%v'", expectedErr, err)
	}
}

func TestUserService_UpdateUser(t *testing.T) {
	t.Run("validation missing user_id", func(t *testing.T) {
		userService := NewUserService(&mockUserRepository{}, nil)
		err := userService.UpdateUser(context.Background(), UpdateUserInput{UserID: "", Name: "Alice"})
		if !errors.Is(err, domain.ErrUserIDRequired) {
			t.Errorf("expected ErrUserIDRequired, got %v", err)
		}
	})

	t.Run("validation missing name", func(t *testing.T) {
		userService := NewUserService(&mockUserRepository{}, nil)
		err := userService.UpdateUser(context.Background(), UpdateUserInput{UserID: "usr_1", Name: ""})
		if !errors.Is(err, domain.ErrUserNameRequired) {
			t.Errorf("expected ErrUserNameRequired, got %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		updated := false
		mockUserRepository := &mockUserRepository{
			updateUserFunc: func(ctx context.Context, input repository.UpdateUserInput) error {
				if input.ID == "usr_1" && input.Name == "Alice Smith" {
					updated = true
				}
				return nil
			},
		}
		userService := NewUserService(mockUserRepository, nil)
		err := userService.UpdateUser(context.Background(), UpdateUserInput{UserID: "usr_1", Name: "Alice Smith"})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if !updated {
			t.Error("expected UpdateUser to be called")
		}
	})
}

func TestUserService_ListUsers(t *testing.T) {
	var gotTenantID string
	mockUserRepository := &mockUserRepository{
		listUsersFunc: func(ctx context.Context, tenantID string) ([]domain.User, error) {
			gotTenantID = tenantID
			return []domain.User{{ID: "usr_1"}}, nil
		},
	}
	userService := NewUserService(mockUserRepository, nil)
	users, err := userService.ListUsers(context.Background(), "tenant-abc")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if gotTenantID != "tenant-abc" {
		t.Fatalf("expected ListUsers to be scoped to tenant-abc, got %q", gotTenantID)
	}
}

func TestUserService_ListUsers_RequiresTenant(t *testing.T) {
	userService := NewUserService(&mockUserRepository{}, nil)
	_, err := userService.ListUsers(context.Background(), "")
	if !errors.Is(err, domain.ErrTenantIDRequired) {
		t.Errorf("expected ErrTenantIDRequired, got %v", err)
	}
}

// TestUserService_ErrorContractInvariants enforces the platform standard that
// sentinel errors are declared ONLY in the pure domain package (Rule 4.1:
// "Domain Purity"). The application service layer must reference domain
// sentinels, never declare its own `var Err...` block.
func TestUserService_ErrorContractInvariants(t *testing.T) {
	// 1. The service layer MUST NOT declare sentinel errors of its own.
	fset := token.NewFileSet()
	serviceNode, err := parser.ParseFile(fset, "user_service.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse user_service.go AST: %v", err)
	}
	for _, decl := range serviceNode.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range valueSpec.Names {
				if strings.HasPrefix(name.Name, "Err") {
					t.Errorf("service layer must NOT declare sentinel error '%s'; move it to internal/domain", name.Name)
				}
			}
		}
	}

	// 2. The domain package MUST declare exported, `Err`-prefixed sentinels.
	domainNode, err := parser.ParseFile(fset, "../domain/errors.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse ../domain/errors.go AST: %v", err)
	}
	var errorVarsFound int
	for _, decl := range domainNode.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range valueSpec.Names {
				if !strings.HasPrefix(name.Name, "Err") {
					t.Errorf("sentinel error variable '%s' must start with prefix 'Err'", name.Name)
				}
				errorVarsFound++
			}
		}
	}
	if errorVarsFound == 0 {
		t.Error("expected at least one sentinel error declaration in internal/domain/errors.go")
	}
}
