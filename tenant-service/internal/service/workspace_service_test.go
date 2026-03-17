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
	createTenantFunc     func(ctx context.Context, input repository.CreateTenantInput) error
	getTenantByIDFunc    func(ctx context.Context, tenantID string) (*domain.Tenant, error)
	activateTenantFunc   func(ctx context.Context, tenantID string) error
	updateTenantFunc     func(ctx context.Context, input repository.UpdateTenantInput) error
	updateTenantPlanFunc func(ctx context.Context, input repository.UpdateTenantPlanInput) error
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

func (m *mockTenantRepository) UpdateTenant(ctx context.Context, input repository.UpdateTenantInput) error {
	if m.updateTenantFunc != nil {
		return m.updateTenantFunc(ctx, input)
	}
	return nil
}

func (m *mockTenantRepository) UpdateTenantPlan(ctx context.Context, input repository.UpdateTenantPlanInput) error {
	if m.updateTenantPlanFunc != nil {
		return m.updateTenantPlanFunc(ctx, input)
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
	var capturedOutbox repository.CreateOutboxMessageInput

	tenantRepo := &mockTenantRepository{
		createTenantFunc: func(ctx context.Context, input repository.CreateTenantInput) error {
			capturedTenant = input
			return nil
		},
	}

	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, input repository.CreateOutboxMessageInput) error {
			capturedOutbox = input
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

func TestWorkspaceService_ListTenants(t *testing.T) {
	t.Run("missing tenant_id", func(t *testing.T) {
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: &mockTenantRepository{}})
		_, err := svc.ListTenants(context.Background(), "")
		if !errors.Is(err, ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		mockRepo := &mockTenantRepository{
			getTenantByIDFunc: func(ctx context.Context, tenantID string) (*domain.Tenant, error) {
				return &domain.Tenant{ID: tenantID, Name: "Acme"}, nil
			},
		}
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: mockRepo})
		tenants, err := svc.ListTenants(context.Background(), "t_100")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if len(tenants) != 1 || tenants[0].ID != "t_100" {
			t.Errorf("unexpected tenants returned: %+v", tenants)
		}
	})
}

func TestWorkspaceService_UpdateTenant(t *testing.T) {
	t.Run("validation error", func(t *testing.T) {
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: &mockTenantRepository{}})
		err := svc.UpdateTenant(context.Background(), UpdateTenantServiceInput{TenantID: "", Name: "New Name"})
		if !errors.Is(err, ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		updated := false
		mockRepo := &mockTenantRepository{
			updateTenantFunc: func(ctx context.Context, input repository.UpdateTenantInput) error {
				if input.ID == "t_100" && input.Name == "New Acme" {
					updated = true
				}
				return nil
			},
		}
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: mockRepo})
		err := svc.UpdateTenant(context.Background(), UpdateTenantServiceInput{
			TenantID:   "t_100",
			Name:       "New Acme",
			OwnerEmail: "owner@acme.com",
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if !updated {
			t.Error("expected UpdateTenant to be called")
		}
	})
}

func TestWorkspaceService_ChangeTenantPlan(t *testing.T) {
	t.Run("invalid plan", func(t *testing.T) {
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: &mockTenantRepository{}})
		err := svc.ChangeTenantPlan(context.Background(), ChangeTenantPlanInput{TenantID: "t_100", Plan: "invalid"})
		if !errors.Is(err, ErrInvalidPlan) {
			t.Errorf("expected ErrInvalidPlan, got %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		changed := false
		mockRepo := &mockTenantRepository{
			updateTenantPlanFunc: func(ctx context.Context, input repository.UpdateTenantPlanInput) error {
				if input.ID == "t_100" && input.Plan == "dedicated" {
					changed = true
				}
				return nil
			},
		}
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: mockRepo})
		err := svc.ChangeTenantPlan(context.Background(), ChangeTenantPlanInput{TenantID: "t_100", Plan: "dedicated"})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if !changed {
			t.Error("expected ChangeTenantPlan to be called")
		}
	})
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
