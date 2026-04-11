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
	"user-service/internal/infrastructure/authclient"
	"user-service/internal/repository"
)

type mockUserRepository struct {
	createUserFunc        func(ctx context.Context, input repository.CreateUserInput) error
	getUserByEmailFunc    func(ctx context.Context, email string) (*domain.User, error)
	updateUserFunc        func(ctx context.Context, input repository.UpdateUserInput) error
	getUserByIDFunc       func(ctx context.Context, userID string) (*domain.User, error)
	listUsersFunc         func(ctx context.Context, tenantID string) ([]domain.User, error)
	addMembershipFunc     func(ctx context.Context, userID, tenantID string) error
	userBelongsToTenantFn func(ctx context.Context, userID, tenantID string) (bool, error)
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

func (m *mockUserRepository) UpdateUser(ctx context.Context, input repository.UpdateUserInput) error {
	if m.updateUserFunc != nil {
		return m.updateUserFunc(ctx, input)
	}
	return nil
}

func (m *mockUserRepository) GetUserByID(ctx context.Context, userID string) (*domain.User, error) {
	if m.getUserByIDFunc != nil {
		return m.getUserByIDFunc(ctx, userID)
	}
	return nil, nil
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

func (m *mockUserRepository) UserBelongsToTenant(ctx context.Context, userID, tenantID string) (bool, error) {
	if m.userBelongsToTenantFn != nil {
		return m.userBelongsToTenantFn(ctx, userID, tenantID)
	}
	return true, nil
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

type mockRoleClient struct {
	assignUserRoleFn  func(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error)
	getUserRoleFn     func(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error)
	createRoleFn      func(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error)
	listRolesFn       func(ctx context.Context, authToken string) ([]authclient.Role, error)
	listPermissionsFn func(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error)
}

func (m *mockRoleClient) AssignUserRole(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error) {
	if m.assignUserRoleFn != nil {
		return m.assignUserRoleFn(ctx, authToken, userID, roleID)
	}
	return nil, nil
}

func (m *mockRoleClient) GetUserRole(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error) {
	if m.getUserRoleFn != nil {
		return m.getUserRoleFn(ctx, authToken, userID)
	}
	return nil, nil
}

func (m *mockRoleClient) CreateRole(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error) {
	if m.createRoleFn != nil {
		return m.createRoleFn(ctx, authToken, input)
	}
	return nil, nil
}

func (m *mockRoleClient) ListRoles(ctx context.Context, authToken string) ([]authclient.Role, error) {
	if m.listRolesFn != nil {
		return m.listRolesFn(ctx, authToken)
	}
	return nil, nil
}

func (m *mockRoleClient) ListPermissions(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error) {
	if m.listPermissionsFn != nil {
		return m.listPermissionsFn(ctx, authToken)
	}
	return nil, nil
}

func TestUserService_CreateUserFromWorkspace_Validation(t *testing.T) {
	svc := NewUserService(&mockUserRepository{}, &mockOutboxRepository{}, nil)

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
	var membershipAdded string

	userRepo := &mockUserRepository{
		createUserFunc: func(ctx context.Context, input repository.CreateUserInput) error {
			createdUser = input
			return nil
		},
		addMembershipFunc: func(ctx context.Context, userID, tenantID string) error {
			membershipAdded = tenantID
			return nil
		},
	}

	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, input repository.CreateOutboxMessageInput) error {
			createdOutbox = input
			return nil
		},
	}

	svc := NewUserService(userRepo, outboxRepo, nil)

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

	if membershipAdded != "tenant-456" {
		t.Errorf("expected tenant membership to be added for tenant-456, got %q", membershipAdded)
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

	svc := NewUserService(userRepo, nil, nil)

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

	svc := NewUserService(userRepo, nil, nil)

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

	svc := NewUserService(userRepo, outboxRepo, nil)

	input := CreateUserFromWorkspaceInput{
		TenantID:   "tenant-123",
		OwnerEmail: "test@example.com",
	}

	err := svc.CreateUserFromWorkspace(context.Background(), input)
	if err == nil || !errors.Is(err, expectedErr) {
		t.Errorf("expected error wrapping '%v', got '%v'", expectedErr, err)
	}
}

func TestUserService_UpdateUser(t *testing.T) {
	t.Run("validation missing user_id", func(t *testing.T) {
		userService := NewUserService(&mockUserRepository{}, nil, nil)
		err := userService.UpdateUser(context.Background(), UpdateUserServiceInput{UserID: "", Name: "Alice"})
		if !errors.Is(err, ErrUserIDRequired) {
			t.Errorf("expected ErrUserIDRequired, got %v", err)
		}
	})

	t.Run("validation missing name", func(t *testing.T) {
		userService := NewUserService(&mockUserRepository{}, nil, nil)
		err := userService.UpdateUser(context.Background(), UpdateUserServiceInput{UserID: "usr_1", Name: ""})
		if !errors.Is(err, ErrUserNameRequired) {
			t.Errorf("expected ErrUserNameRequired, got %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		updated := false
		mockRepo := &mockUserRepository{
			updateUserFunc: func(ctx context.Context, input repository.UpdateUserInput) error {
				if input.ID == "usr_1" && input.Name == "Alice Smith" {
					updated = true
				}
				return nil
			},
		}
		userService := NewUserService(mockRepo, nil, nil)
		err := userService.UpdateUser(context.Background(), UpdateUserServiceInput{UserID: "usr_1", Name: "Alice Smith"})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if !updated {
			t.Error("expected UpdateUser to be called")
		}
	})
}

func TestUserService_GetUserByID(t *testing.T) {
	t.Run("missing user_id", func(t *testing.T) {
		userService := NewUserService(&mockUserRepository{}, nil, nil)
		_, err := userService.GetUserByID(context.Background(), "")
		if !errors.Is(err, ErrUserIDRequired) {
			t.Errorf("expected ErrUserIDRequired, got %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		mockRepo := &mockUserRepository{
			getUserByIDFunc: func(ctx context.Context, userID string) (*domain.User, error) {
				return &domain.User{ID: userID, Name: "Bob"}, nil
			},
		}
		userService := NewUserService(mockRepo, nil, nil)
		u, err := userService.GetUserByID(context.Background(), "usr_2")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if u.ID != "usr_2" || u.Name != "Bob" {
			t.Errorf("unexpected user returned: %+v", u)
		}
	})
}

func TestUserService_ListUsers(t *testing.T) {
	var gotTenantID string
	mockRepo := &mockUserRepository{
		listUsersFunc: func(ctx context.Context, tenantID string) ([]domain.User, error) {
			gotTenantID = tenantID
			return []domain.User{{ID: "usr_1"}}, nil
		},
	}
	userService := NewUserService(mockRepo, nil, nil)
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
	userService := NewUserService(&mockUserRepository{}, nil, nil)
	_, err := userService.ListUsers(context.Background(), "")
	if !errors.Is(err, ErrTenantIDRequired) {
		t.Errorf("expected ErrTenantIDRequired, got %v", err)
	}
}

func TestUserService_RoleDelegation(t *testing.T) {
	t.Run("nil role client returns error", func(t *testing.T) {
		userService := NewUserService(&mockUserRepository{}, nil, nil)
		_, err := userService.AssignUserRole(context.Background(), "token", "u1", "r1", "t1")
		if err == nil {
			t.Error("expected error when roleClient is nil")
		}
	})

	t.Run("rejects cross-tenant target user", func(t *testing.T) {
		mockRole := &mockRoleClient{
			assignUserRoleFn: func(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error) {
				return &authclient.UserRoleResponse{UserID: userID, RoleID: roleID}, nil
			},
		}
		userRepo := &mockUserRepository{
			userBelongsToTenantFn: func(ctx context.Context, userID, tenantID string) (bool, error) {
				return false, nil
			},
		}
		userService := NewUserService(userRepo, nil, mockRole)
		if _, err := userService.AssignUserRole(context.Background(), "t", "u1", "r1", "tenant-a"); err == nil {
			t.Error("expected error when target user is not a member of the tenant")
		}
	})

	t.Run("successful delegation to role client", func(t *testing.T) {
		mockRole := &mockRoleClient{
			assignUserRoleFn: func(ctx context.Context, authToken, userID, roleID string) (*authclient.UserRoleResponse, error) {
				return &authclient.UserRoleResponse{UserID: userID, RoleID: roleID}, nil
			},
			getUserRoleFn: func(ctx context.Context, authToken, userID string) (*authclient.UserRoleResponse, error) {
				return &authclient.UserRoleResponse{UserID: userID, RoleName: "admin"}, nil
			},
			createRoleFn: func(ctx context.Context, authToken string, input authclient.CreateRoleInput) (*authclient.Role, error) {
				return &authclient.Role{ID: "r1", Name: input.Name}, nil
			},
			listRolesFn: func(ctx context.Context, authToken string) ([]authclient.Role, error) {
				return []authclient.Role{{ID: "r1"}}, nil
			},
			listPermissionsFn: func(ctx context.Context, authToken string) ([]authclient.PermissionCatalogItem, error) {
				return []authclient.PermissionCatalogItem{{ID: "p1"}}, nil
			},
		}

		userService := NewUserService(&mockUserRepository{}, nil, mockRole)
		ctx := context.Background()

		if _, err := userService.AssignUserRole(ctx, "t", "u1", "r1", "tenant-a"); err != nil {
			t.Errorf("AssignUserRole failed: %v", err)
		}
		if _, err := userService.GetUserRole(ctx, "t", "u1", "tenant-a"); err != nil {
			t.Errorf("GetUserRole failed: %v", err)
		}
		if _, err := userService.CreateRole(ctx, "t", authclient.CreateRoleInput{Name: "role"}); err != nil {
			t.Errorf("CreateRole failed: %v", err)
		}
		if _, err := userService.ListRoles(ctx, "t"); err != nil {
			t.Errorf("ListRoles failed: %v", err)
		}
		if _, err := userService.ListPermissions(ctx, "t"); err != nil {
			t.Errorf("ListPermissions failed: %v", err)
		}
	})
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
