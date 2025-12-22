package service

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"tenant-service/internal/domain"
	"tenant-service/internal/repository"
)

type mockTenantRepository struct {
	createTenantFunc   func(ctx context.Context, input repository.CreateTenantInput) error
	getTenantByIDFunc  func(ctx context.Context, tenantID string) (*domain.Tenant, error)
	activateTenantFunc func(ctx context.Context, tenantID string) error
}

func (m *mockTenantRepository) CreateTenant(ctx context.Context, input repository.CreateTenantInput) error {
	if m.createTenantFunc != nil {
		return m.createTenantFunc(ctx, input)
	}
	return nil
}

func (m *mockTenantRepository) GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error) {
	if m.getTenantByIDFunc != nil {
		return m.getTenantByIDFunc(ctx, tenantID)
	}
	return &domain.Tenant{ID: tenantID, Status: "PENDING", OwnerEmail: "owner@test.com"}, nil
}

func (m *mockTenantRepository) ActivateTenant(ctx context.Context, tenantID string) error {
	if m.activateTenantFunc != nil {
		return m.activateTenantFunc(ctx, tenantID)
	}
	return nil
}

type mockOutboxRepository struct {
	createOutboxMessageFunc func(ctx context.Context, msg domain.OutboxMessage) error
}

func (m *mockOutboxRepository) CreateOutboxMessage(ctx context.Context, msg domain.OutboxMessage) error {
	if m.createOutboxMessageFunc != nil {
		return m.createOutboxMessageFunc(ctx, msg)
	}
	return nil
}

type mockOutboxWorker struct {
	poked bool
}

func (m *mockOutboxWorker) Poke() {
	m.poked = true
}

func TestWorkspaceService_RegisterWorkspace_Validation(t *testing.T) {
	svc := NewWorkspaceService(WorkspaceServiceParams{
		TenantRepository: &mockTenantRepository{},
		OutboxRepository: &mockOutboxRepository{},
		OutboxWorker:     &mockOutboxWorker{},
	})

	tests := []struct {
		name    string
		input   RegisterWorkspaceInput
		wantErr error
	}{
		{
			name: "missing owner_email",
			input: RegisterWorkspaceInput{
				OwnerEmail: "",
				TenantName: "My Company",
				Plan:       "shared",
			},
			wantErr: ErrOwnerEmailRequired,
		},
		{
			name: "missing tenant_name",
			input: RegisterWorkspaceInput{
				OwnerEmail: "owner@company.com",
				TenantName: "",
				Plan:       "shared",
			},
			wantErr: ErrTenantNameRequired,
		},
		{
			name: "invalid plan",
			input: RegisterWorkspaceInput{
				OwnerEmail: "owner@company.com",
				TenantName: "My Company",
				Plan:       "enterprise_invalid",
			},
			wantErr: ErrInvalidPlan,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := svc.RegisterWorkspace(context.Background(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
			if output != nil {
				t.Errorf("expected nil output on validation failure, got %v", output)
			}
		})
	}
}

func TestWorkspaceService_RegisterWorkspace_Success(t *testing.T) {
	var capturedTenant repository.CreateTenantInput
	var capturedOutbox domain.OutboxMessage

	tenantRepo := &mockTenantRepository{
		createTenantFunc: func(ctx context.Context, input repository.CreateTenantInput) error {
			capturedTenant = input
			return nil
		},
	}

	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, msg domain.OutboxMessage) error {
			capturedOutbox = msg
			return nil
		},
	}

	worker := &mockOutboxWorker{}
	svc := NewWorkspaceService(WorkspaceServiceParams{
		TenantRepository: tenantRepo,
		OutboxRepository: outboxRepo,
		OutboxWorker:     worker,
	})

	input := RegisterWorkspaceInput{
		OwnerEmail: "owner@acme.com",
		OwnerName:  "Bob Jones",
		TenantName: "Acme Corp",
		Plan:       "DEDICATED",
	}

	out, err := svc.RegisterWorkspace(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.TenantID == "" {
		t.Error("expected non-empty TenantID")
	}
	if capturedTenant.Name != "Acme Corp" || capturedTenant.Plan != "dedicated" {
		t.Errorf("unexpected tenant record: %+v", capturedTenant)
	}
	if capturedOutbox.EventType != "workspace.initiated" || capturedOutbox.AggregateID != out.TenantID {
		t.Errorf("unexpected outbox message: %+v", capturedOutbox)
	}
	if !worker.poked {
		t.Error("expected outbox worker to be poked after registration")
	}
}

func TestWorkspaceService_RegisterWorkspace_OutboxRepoError(t *testing.T) {
	expectedErr := errors.New("outbox failed")
	tenantRepo := &mockTenantRepository{}
	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, msg domain.OutboxMessage) error {
			return expectedErr
		},
	}

	svc := NewWorkspaceService(WorkspaceServiceParams{
		TenantRepository: tenantRepo,
		OutboxRepository: outboxRepo,
	})

	input := RegisterWorkspaceInput{
		OwnerEmail: "owner@test.com",
		TenantName: "Test Workspace",
		Plan:       "shared",
	}

	_, err := svc.RegisterWorkspace(context.Background(), input)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestWorkspaceService_ActivateWorkspace_ValidationAndNotFound(t *testing.T) {
	svc := NewWorkspaceService(WorkspaceServiceParams{
		TenantRepository: &mockTenantRepository{
			getTenantByIDFunc: func(ctx context.Context, tenantID string) (*domain.Tenant, error) {
				return nil, errors.New("tenant not found")
			},
		},
	})

	t.Run("missing tenant_id", func(t *testing.T) {
		err := svc.ActivateWorkspace(context.Background(), "")
		if !errors.Is(err, ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}
	})

	t.Run("tenant not found", func(t *testing.T) {
		err := svc.ActivateWorkspace(context.Background(), "tenant-missing")
		if !errors.Is(err, ErrTenantNotFound) {
			t.Errorf("expected ErrTenantNotFound, got %v", err)
		}
	})
}

func TestWorkspaceService_ActivateWorkspace_Success(t *testing.T) {
	var activatedTenantID string
	var capturedOutbox domain.OutboxMessage
	worker := &mockOutboxWorker{}

	tenantRepo := &mockTenantRepository{
		getTenantByIDFunc: func(ctx context.Context, tenantID string) (*domain.Tenant, error) {
			return &domain.Tenant{ID: tenantID, OwnerEmail: "owner@active.com"}, nil
		},
		activateTenantFunc: func(ctx context.Context, tenantID string) error {
			activatedTenantID = tenantID
			return nil
		},
	}

	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, msg domain.OutboxMessage) error {
			capturedOutbox = msg
			return nil
		},
	}

	svc := NewWorkspaceService(WorkspaceServiceParams{
		TenantRepository: tenantRepo,
		OutboxRepository: outboxRepo,
		OutboxWorker:     worker,
	})

	err := svc.ActivateWorkspace(context.Background(), "t-100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if activatedTenantID != "t-100" {
		t.Errorf("expected activated tenantID 't-100', got '%s'", activatedTenantID)
	}
	if capturedOutbox.EventType != "workspace.ready" || capturedOutbox.AggregateID != "t-100" {
		t.Errorf("unexpected outbox event: %+v", capturedOutbox)
	}
	if !worker.poked {
		t.Error("expected outbox worker to be poked on workspace activation")
	}
}

func TestTenantService_ErrorContractInvariants(t *testing.T) {
	files := map[string]string{
		"workspace_service.go":            "workspace service:",
		"tenant_infrastructure_service.go": "tenant infrastructure service:",
	}

	for fileName, expectedPrefix := range files {
		t.Run(fileName, func(t *testing.T) {
			fset := token.NewFileSet()
			node, err := parser.ParseFile(fset, fileName, nil, parser.ParseComments)
			if err != nil {
				t.Fatalf("failed to parse %s AST: %v", fileName, err)
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
							t.Errorf("sentinel error variable '%s' in %s must start with prefix 'Err'", name.Name, fileName)
						}
						errorVarsFound++
						if i < len(valueSpec.Values) {
							if call, ok := valueSpec.Values[i].(*ast.CallExpr); ok {
								if len(call.Args) > 0 {
									if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
										errStr := strings.Trim(lit.Value, `"`)
										if !strings.HasPrefix(errStr, expectedPrefix) {
											t.Errorf("sentinel error '%s' message '%s' in %s must start with prefix '%s'", name.Name, errStr, fileName, expectedPrefix)
										}
									}
								}
							}
						}
					}
				}
			}
			if errorVarsFound == 0 {
				t.Errorf("expected at least one sentinel error declaration in %s", fileName)
			}
		})
	}
}
