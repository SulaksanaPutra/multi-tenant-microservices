package service

import (
	"context"
	"encoding/json"
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
			wantErr: domain.ErrOwnerEmailRequired,
		},
		{
			name: "missing tenant_name",
			input: RegisterWorkspaceInput{
				OwnerEmail: "owner@company.com",
				TenantName: "",
				Plan:       "shared",
			},
			wantErr: domain.ErrTenantNameRequired,
		},
		{
			name: "invalid plan",
			input: RegisterWorkspaceInput{
				OwnerEmail: "owner@company.com",
				TenantName: "My Company",
				Plan:       "enterprise_invalid",
			},
			wantErr: domain.ErrInvalidPlan,
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

func TestWorkspaceService_ActivateWorkspace_EmitsTenantInfo(t *testing.T) {
	var capturedOutbox repository.CreateOutboxMessageInput

	tenantRepo := &mockTenantRepository{
		getTenantByIDFunc: func(ctx context.Context, tenantID string) (*domain.Tenant, error) {
			return &domain.Tenant{
				ID:         tenantID,
				Name:       "Acme Corp",
				Slug:       "acme-corp",
				OwnerEmail: "owner@acme.com",
				OwnerName:  "Bob Jones",
				Plan:       "shared",
				Status:     "pending",
			}, nil
		},
		activateTenantFunc: func(ctx context.Context, tenantID string) error {
			return nil
		},
	}

	outboxRepo := &mockOutboxRepository{
		createOutboxMessageFunc: func(ctx context.Context, input repository.CreateOutboxMessageInput) error {
			capturedOutbox = input
			return nil
		},
	}

	svc := NewWorkspaceService(WorkspaceServiceParams{
		TenantRepository: tenantRepo,
		OutboxRepository: outboxRepo,
		OutboxWorker:     &mockOutboxWorker{},
	})

	if err := svc.ActivateWorkspace(context.Background(), "tnt_acme"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedOutbox.EventType != "workspace.ready" {
		t.Fatalf("expected workspace.ready outbox message, got %+v", capturedOutbox)
	}

	var evt domain.WorkspaceReadyEvent
	if err := json.Unmarshal(capturedOutbox.Payload, &evt); err != nil {
		t.Fatalf("failed to unmarshal WorkspaceReady payload: %v", err)
	}
	if evt.TenantID != "tnt_acme" {
		t.Errorf("expected TenantID 'tnt_acme', got '%s'", evt.TenantID)
	}
	if evt.TenantName != "Acme Corp" {
		t.Errorf("expected TenantName 'Acme Corp', got '%s'", evt.TenantName)
	}
	if evt.TenantSlug != "acme-corp" {
		t.Errorf("expected TenantSlug 'acme-corp', got '%s'", evt.TenantSlug)
	}
	if evt.OwnerName != "Bob Jones" {
		t.Errorf("expected OwnerName 'Bob Jones', got '%s'", evt.OwnerName)
	}
	if evt.OwnerEmail != "owner@acme.com" {
		t.Errorf("expected OwnerEmail 'owner@acme.com', got '%s'", evt.OwnerEmail)
	}
}

func TestWorkspaceService_UpdateTenant(t *testing.T) {
	t.Run("validation error", func(t *testing.T) {
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: &mockTenantRepository{}})
		err := svc.UpdateTenant(context.Background(), UpdateTenantServiceInput{TenantID: "", Name: "New Name"})
		if !errors.Is(err, domain.ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		updated := false
		var capturedInput repository.UpdateTenantInput
		mockRepo := &mockTenantRepository{
			updateTenantFunc: func(ctx context.Context, input repository.UpdateTenantInput) error {
				capturedInput = input
				if input.ID == "t_100" && input.Name == "New Acme" {
					updated = true
				}
				return nil
			},
		}
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: mockRepo})
		ownerEmail := "owner@acme.com"
		err := svc.UpdateTenant(context.Background(), UpdateTenantServiceInput{
			TenantID:   "t_100",
			Name:       "New Acme",
			OwnerEmail: &ownerEmail,
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if !updated {
			t.Error("expected UpdateTenant to be called")
		}
		if capturedInput.OwnerEmail == nil || *capturedInput.OwnerEmail != "owner@acme.com" {
			t.Errorf("expected OwnerEmail to be passed through, got %v", capturedInput.OwnerEmail)
		}
		if capturedInput.OwnerName != nil {
			t.Errorf("expected OwnerName to remain nil when not provided, got %v", *capturedInput.OwnerName)
		}
	})

	t.Run("owner fields omitted leaves repository input nil", func(t *testing.T) {
		var capturedInput repository.UpdateTenantInput
		mockRepo := &mockTenantRepository{
			updateTenantFunc: func(ctx context.Context, input repository.UpdateTenantInput) error {
				capturedInput = input
				return nil
			},
		}
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: mockRepo})
		err := svc.UpdateTenant(context.Background(), UpdateTenantServiceInput{
			TenantID: "t_100",
			Name:     "New Acme",
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if capturedInput.OwnerEmail != nil || capturedInput.OwnerName != nil {
			t.Errorf("expected owner fields to remain nil, got OwnerEmail=%v OwnerName=%v", capturedInput.OwnerEmail, capturedInput.OwnerName)
		}
	})
}

func TestWorkspaceService_ChangeTenantPlan(t *testing.T) {
	t.Run("invalid plan", func(t *testing.T) {
		svc := NewWorkspaceService(WorkspaceServiceParams{TenantRepository: &mockTenantRepository{}})
		err := svc.ChangeTenantPlan(context.Background(), ChangeTenantPlanInput{TenantID: "t_100", Plan: "invalid"})
		if !errors.Is(err, domain.ErrInvalidPlan) {
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
	// Sentinel errors were centralized into the domain package by the
	// "centralize service errors" refactor; each message still carries the
	// owning service/domain prefix.
	knownPrefixes := []string{
		"domain:",
		"workspace service:",
		"tenant infrastructure service:",
	}

	const errorsFile = "../domain/errors.go"

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, errorsFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse %s AST: %v", errorsFile, err)
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
					t.Errorf("sentinel error variable '%s' in %s must start with prefix 'Err'", name.Name, errorsFile)
				}
				errorVarsFound++
				if i < len(valueSpec.Values) {
					if call, ok := valueSpec.Values[i].(*ast.CallExpr); ok {
						if len(call.Args) > 0 {
							if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
								errStr := strings.Trim(lit.Value, `"`)
								matched := false
								for _, prefix := range knownPrefixes {
									if strings.HasPrefix(errStr, prefix) {
										matched = true
										break
									}
								}
								if !matched {
									t.Errorf("sentinel error '%s' message '%s' in %s must start with a known service prefix (%v)", name.Name, errStr, errorsFile, knownPrefixes)
								}
							}
						}
					}
				}
			}
		}
	}
	if errorVarsFound == 0 {
		t.Errorf("expected at least one sentinel error declaration in %s", errorsFile)
	}
}
