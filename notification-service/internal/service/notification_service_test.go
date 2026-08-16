package service

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"notification-service/internal/domain"
	"notification-service/internal/repository"
)

type mockNotificationRepo struct {
	createNotificationLogFunc    func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error)
	updateNotificationStatusFunc func(ctx context.Context, id string, status string) error
	hasSentNotificationFunc      func(ctx context.Context, tenantID string) (bool, error)
	getPendingNotificationFunc   func(ctx context.Context, tenantID string) (*domain.NotificationLog, error)
	listNotificationsFunc        func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error)
}

func (m *mockNotificationRepo) CreateNotificationLog(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
	if m.createNotificationLogFunc != nil {
		return m.createNotificationLogFunc(ctx, input)
	}
	return "ntf_1", nil
}

func (m *mockNotificationRepo) UpdateNotificationStatus(ctx context.Context, id string, status string) error {
	if m.updateNotificationStatusFunc != nil {
		return m.updateNotificationStatusFunc(ctx, id, status)
	}
	return nil
}

func (m *mockNotificationRepo) HasSentNotification(ctx context.Context, tenantID string) (bool, error) {
	if m.hasSentNotificationFunc != nil {
		return m.hasSentNotificationFunc(ctx, tenantID)
	}
	return false, nil
}

func (m *mockNotificationRepo) GetPendingNotification(ctx context.Context, tenantID string) (*domain.NotificationLog, error) {
	if m.getPendingNotificationFunc != nil {
		return m.getPendingNotificationFunc(ctx, tenantID)
	}
	return nil, nil
}

func (m *mockNotificationRepo) ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
	if m.listNotificationsFunc != nil {
		return m.listNotificationsFunc(ctx, tenantID)
	}
	return nil, nil
}

func newSvc() *NotificationService {
	return NewNotificationService(&mockNotificationRepo{})
}

func userCreatedPayload(userID, email string) []byte {
	b, _ := json.Marshal(map[string]string{"user_id": userID, "email": email})
	return b
}

func workspaceReadyPayload(ownerEmail string) []byte {
	return workspaceReadyPayloadWithTenant(ownerEmail, "", "", "")
}

func workspaceReadyPayloadWithTenant(ownerEmail, tenantName, tenantSlug, ownerName string) []byte {
	b, _ := json.Marshal(map[string]string{
		"owner_email": ownerEmail,
		"tenant_name": tenantName,
		"tenant_slug": tenantSlug,
		"owner_name":  ownerName,
	})
	return b
}

func bothBarrierEvents() []domain.InboxMessage {
	return []domain.InboxMessage{
		{EventType: "user.created", Payload: userCreatedPayload("usr_123", "owner@company.com")},
		{EventType: "workspace.ready", Payload: workspaceReadyPayload("owner@company.com")},
	}
}

func TestProcessEventAndTrySendWelcome_Validation(t *testing.T) {
	svc := newSvc()

	t.Run("missing event_id", func(t *testing.T) {
		_, err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{TenantID: "tenant-1"}, nil)
		if !errors.Is(err, domain.ErrEventIDRequired) {
			t.Errorf("expected ErrEventIDRequired, got %v", err)
		}
	})

	t.Run("missing tenant_id", func(t *testing.T) {
		_, err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "evt-1"}, nil)
		if !errors.Is(err, domain.ErrTenantIDRequired) {
			t.Errorf("expected domain.ErrTenantIDRequired, got %v", err)
		}
	})
}

func TestProcessEventAndTrySendWelcome_WaitingBarrierCondition(t *testing.T) {
	// Only user.created present — workspace.ready missing.
	events := []domain.InboxMessage{
		{EventType: "user.created", Payload: userCreatedPayload("usr_123", "owner@company.com")},
	}

	svc := newSvc()
	details, err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
		EventID:   "evt-1",
		TenantID:  "tenant-1",
		EventType: "user.created",
	}, events)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if details != nil {
		t.Error("expected nil ProcessEventOutput when barrier condition not met")
	}
}

func TestProcessEventAndTrySendWelcome_BarrierMet_ReturnsDetails(t *testing.T) {
	var capturedLog repository.CreateNotificationLogInput
	notifRepo := &mockNotificationRepo{
		createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
			capturedLog = input
			return "ntf_42", nil
		},
	}
	svc := NewNotificationService(notifRepo)

	details, err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
		EventID:   "evt-2",
		TenantID:  "tenant-1",
		EventType: "workspace.ready",
	}, bothBarrierEvents())

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if details == nil {
		t.Fatal("expected non-nil ProcessEventOutput when barrier met")
	}
	if details.LogID != "ntf_42" {
		t.Errorf("expected LogID=ntf_42, got %s", details.LogID)
	}
	if details.RecipientEmail != "owner@company.com" {
		t.Errorf("unexpected recipient email: %s", details.RecipientEmail)
	}
	// Audit log must be written with status "pending" — SMTP confirmation happens post-commit.
	if capturedLog.Status != "pending" {
		t.Errorf("expected audit log status 'pending', got '%s'", capturedLog.Status)
	}
}

func TestProcessEventAndTrySendWelcome_AlreadySent(t *testing.T) {
	notifRepo := &mockNotificationRepo{
		hasSentNotificationFunc: func(ctx context.Context, tenantID string) (bool, error) {
			return true, nil // already sent
		},
	}
	svc := NewNotificationService(notifRepo)

	details, err := svc.ProcessEventAndTrySendWelcome(context.Background(),
		ProcessEventInput{EventID: "e-1", TenantID: "tenant-1"},
		bothBarrierEvents())

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if details != nil {
		t.Error("expected nil ProcessEventOutput when already sent")
	}
}

func TestProcessEventAndTrySendWelcome_Errors(t *testing.T) {
	expectedErr := errors.New("infra failure")

	t.Run("HasSentNotification error", func(t *testing.T) {
		notifRepo := &mockNotificationRepo{
			hasSentNotificationFunc: func(ctx context.Context, tenantID string) (bool, error) {
				return false, expectedErr
			},
		}
		svc := NewNotificationService(notifRepo)
		_, err := svc.ProcessEventAndTrySendWelcome(context.Background(),
			ProcessEventInput{EventID: "e-1", TenantID: "t-1"},
			bothBarrierEvents())
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("GetPendingNotification error", func(t *testing.T) {
		notifRepo := &mockNotificationRepo{
			getPendingNotificationFunc: func(ctx context.Context, tenantID string) (*domain.NotificationLog, error) {
				return nil, expectedErr
			},
		}
		svc := NewNotificationService(notifRepo)
		_, err := svc.ProcessEventAndTrySendWelcome(context.Background(),
			ProcessEventInput{EventID: "e-1", TenantID: "t-1"},
			bothBarrierEvents())
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("CreateNotificationLog error", func(t *testing.T) {
		notifRepo := &mockNotificationRepo{
			createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
				return "", expectedErr
			},
		}
		svc := NewNotificationService(notifRepo)
		_, err := svc.ProcessEventAndTrySendWelcome(context.Background(),
			ProcessEventInput{EventID: "e-1", TenantID: "t-1"},
			bothBarrierEvents())
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestProcessEventAndTrySendWelcome_ReusesExistingPendingLog(t *testing.T) {
	createCalled := false
	notifRepo := &mockNotificationRepo{
		getPendingNotificationFunc: func(ctx context.Context, tenantID string) (*domain.NotificationLog, error) {
			return &domain.NotificationLog{
				ID:       "ntf_existing_pending_1",
				TenantID: tenantID,
				Status:   "pending",
			}, nil
		},
		createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
			createCalled = true
			return "ntf_should_not_be_called", nil
		},
	}
	svc := NewNotificationService(notifRepo)

	details, err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
		EventID:   "evt-retry",
		TenantID:  "tenant-retry",
		EventType: "user.created",
	}, bothBarrierEvents())

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if details == nil {
		t.Fatal("expected non-nil ProcessEventOutput when barrier met")
	}
	if details.LogID != "ntf_existing_pending_1" {
		t.Errorf("expected reused LogID 'ntf_existing_pending_1', got '%s'", details.LogID)
	}
	if createCalled {
		t.Errorf("expected CreateNotificationLog NOT to be called when pending log already exists")
	}
}

func TestNotificationService_HasSentNotification(t *testing.T) {
	t.Run("empty tenant_id returns ErrTenantIDRequired", func(t *testing.T) {
		svc := newSvc()
		_, err := svc.HasSentNotification(context.Background(), "")
		if !errors.Is(err, domain.ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}
	})

	t.Run("delegates to repo", func(t *testing.T) {
		notifRepo := &mockNotificationRepo{
			hasSentNotificationFunc: func(ctx context.Context, tenantID string) (bool, error) {
				return true, nil
			},
		}
		svc := NewNotificationService(notifRepo)
		sent, err := svc.HasSentNotification(context.Background(), "t-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !sent {
			t.Errorf("expected sent=true, got false")
		}
	})
}

func TestProcessEventAndTrySendWelcome_PayloadFallback(t *testing.T) {
	// Corrupt JSON payloads should trigger fallback to ProcessEventInput values.
	events := []domain.InboxMessage{
		{EventType: "user.created", Payload: []byte("invalid-json")},
		{EventType: "workspace.ready", Payload: []byte("{invalid}")},
	}

	var capturedLog repository.CreateNotificationLogInput
	notifRepo := &mockNotificationRepo{
		createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
			capturedLog = input
			return "ntf_1", nil
		},
	}
	svc := NewNotificationService(notifRepo)

	input := ProcessEventInput{
		EventID:    "evt-fallback",
		TenantID:   "t-fallback",
		UserID:     "usr_explicit",
		OwnerEmail: "explicit@domain.com",
	}

	details, err := svc.ProcessEventAndTrySendWelcome(context.Background(), input, events)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if details == nil {
		t.Fatal("expected non-nil ProcessEventOutput")
	}
	if capturedLog.UserID != "usr_explicit" {
		t.Errorf("expected fallback to input values, got %+v", capturedLog)
	}
	if details.RecipientEmail != "explicit@domain.com" {
		t.Errorf("expected fallback recipient email, got '%s'", details.RecipientEmail)
	}
}

func TestProcessEventAndTrySendWelcome_BarrierMet_IncludesTenantInfo(t *testing.T) {
	var capturedLog repository.CreateNotificationLogInput
	notifRepo := &mockNotificationRepo{
		createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
			capturedLog = input
			return "ntf_7", nil
		},
	}
	svc := NewNotificationService(notifRepo)

	events := []domain.InboxMessage{
		{EventType: "user.created", Payload: userCreatedPayload("usr_123", "owner@acme.com")},
		{EventType: "workspace.ready", Payload: workspaceReadyPayloadWithTenant("owner@acme.com", "Acme Corp", "acme-corp", "Bob Jones")},
	}

	details, err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
		EventID:   "evt-tenant-info",
		TenantID:  "tenant-acme",
		EventType: "workspace.ready",
	}, events)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if details == nil {
		t.Fatal("expected non-nil ProcessEventOutput")
	}
	if details.TenantName != "Acme Corp" || details.TenantSlug != "acme-corp" || details.OwnerName != "Bob Jones" {
		t.Errorf("unexpected tenant info in output: %+v", details)
	}
	if capturedLog.Description != "Welcome to Acme Corp!" {
		t.Errorf("expected description 'Welcome to Acme Corp!', got '%s'", capturedLog.Description)
	}
	if !strings.Contains(capturedLog.Body, "Hello Bob Jones,") {
		t.Errorf("expected body to greet 'Hello Bob Jones,', got:\n%s", capturedLog.Body)
	}
	if !strings.Contains(capturedLog.Body, `workspace "Acme Corp"`) {
		t.Errorf("expected body to reference workspace \"Acme Corp\", got:\n%s", capturedLog.Body)
	}
	if !strings.Contains(capturedLog.Body, "Workspace slug: acme-corp") {
		t.Errorf("expected body to include workspace slug, got:\n%s", capturedLog.Body)
	}
}

func TestProcessEventAndTrySendWelcome_TenantInfoFallback(t *testing.T) {
	var capturedLog repository.CreateNotificationLogInput
	notifRepo := &mockNotificationRepo{
		createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
			capturedLog = input
			return "ntf_8", nil
		},
	}
	svc := NewNotificationService(notifRepo)

	// Legacy workspace.ready payloads do not carry tenant_name/tenant_slug/owner_name.
	events := []domain.InboxMessage{
		{EventType: "user.created", Payload: userCreatedPayload("usr_123", "owner@legacy.com")},
		{EventType: "workspace.ready", Payload: workspaceReadyPayload("owner@legacy.com")},
	}

	details, err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
		EventID:   "evt-legacy",
		TenantID:  "tenant-legacy",
		EventType: "workspace.ready",
	}, events)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if details == nil {
		t.Fatal("expected non-nil ProcessEventOutput")
	}
	if details.TenantName != "" || details.TenantSlug != "" || details.OwnerName != "" {
		t.Errorf("expected empty tenant info for legacy payload, got: %+v", details)
	}
	if capturedLog.Description != "Welcome! Your Tenant Workspace is Ready" {
		t.Errorf("expected generic fallback description, got '%s'", capturedLog.Description)
	}
	if !strings.Contains(capturedLog.Body, "Hello,") {
		t.Errorf("expected generic greeting fallback, got:\n%s", capturedLog.Body)
	}
	if !strings.Contains(capturedLog.Body, `workspace "tenant-legacy"`) {
		t.Errorf("expected body to fall back to tenant ID, got:\n%s", capturedLog.Body)
	}
}

func TestNotificationService_ListNotifications(t *testing.T) {
	t.Run("returns notifications list", func(t *testing.T) {
		expectedLogs := []NotificationLogOutput{
			{ID: "ntf_1", TenantID: "t-1", Description: "Welcome"},
		}
		notifRepo := &mockNotificationRepo{
			listNotificationsFunc: func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
				return []domain.NotificationLog{
					{ID: "ntf_1", TenantID: "t-1", Description: "Welcome"},
				}, nil
			},
		}
		svc := NewNotificationService(notifRepo)

		logs, err := svc.ListNotifications(context.Background(), "t-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(logs, expectedLogs) {
			t.Errorf("expected logs %v, got %v", expectedLogs, logs)
		}
	})

	t.Run("returns repo error", func(t *testing.T) {
		expectedErr := errors.New("db fail")
		notifRepo := &mockNotificationRepo{
			listNotificationsFunc: func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
				return nil, expectedErr
			},
		}
		svc := NewNotificationService(notifRepo)

		_, err := svc.ListNotifications(context.Background(), "t-1")
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestNotificationService_CreateOrderNotification(t *testing.T) {
	t.Run("persists notification with order details", func(t *testing.T) {
		var captured repository.CreateNotificationLogInput
		notifRepo := &mockNotificationRepo{
			createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
				captured = input
				return "ntf_order_1", nil
			},
		}
		svc := NewNotificationService(notifRepo)

		err := svc.CreateOrderNotification(context.Background(), domain.OrderCreatedEvent{
			EventID:    "evt-1",
			TenantID:   "tenant-1",
			OrderID:    "order-1",
			CustomerID: "customer-1",
			Amount:     99.5,
			Status:     "pending",
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if captured.TenantID != "tenant-1" {
			t.Errorf("expected tenant_id 'tenant-1', got '%s'", captured.TenantID)
		}
		if captured.UserID != "customer-1" {
			t.Errorf("expected user_id 'customer-1', got '%s'", captured.UserID)
		}
		if captured.Status != "sent" {
			t.Errorf("expected status 'sent', got '%s'", captured.Status)
		}
		if !strings.Contains(captured.Description, "order-1") {
			t.Errorf("expected description to reference order-1, got '%s'", captured.Description)
		}
		if !strings.Contains(captured.Body, "99.50") {
			t.Errorf("expected body to contain amount, got '%s'", captured.Body)
		}
	})

	t.Run("missing tenant_id returns ErrTenantIDRequired", func(t *testing.T) {
		svc := newSvc()
		err := svc.CreateOrderNotification(context.Background(), domain.OrderCreatedEvent{
			EventID:  "evt-1",
			OrderID:  "order-1",
			Amount:   10,
			Status:   "pending",
		})
		if !errors.Is(err, domain.ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}
	})

	t.Run("falls back to usr_unknown when customer_id empty", func(t *testing.T) {
		var captured repository.CreateNotificationLogInput
		notifRepo := &mockNotificationRepo{
			createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
				captured = input
				return "ntf_order_2", nil
			},
		}
		svc := NewNotificationService(notifRepo)

		err := svc.CreateOrderNotification(context.Background(), domain.OrderCreatedEvent{
			EventID:   "evt-2",
			TenantID:  "tenant-1",
			OrderID:   "order-2",
			Amount:    10,
			Status:    "pending",
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if captured.UserID != "usr_unknown" {
			t.Errorf("expected fallback user_id 'usr_unknown', got '%s'", captured.UserID)
		}
	})

	t.Run("repo error propagates", func(t *testing.T) {
		expectedErr := errors.New("db down")
		notifRepo := &mockNotificationRepo{
			createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (string, error) {
				return "", expectedErr
			},
		}
		svc := NewNotificationService(notifRepo)

		err := svc.CreateOrderNotification(context.Background(), domain.OrderCreatedEvent{
			EventID:  "evt-3",
			TenantID: "tenant-1",
			OrderID:  "order-3",
			Amount:   10,
			Status:   "pending",
		})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected %v, got %v", expectedErr, err)
		}
	})
}

func TestNotificationService_ErrorContractInvariants(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "../domain/errors.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse domain/errors.go AST: %v", err)
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
								if name.Name != "ErrNotFound" && !strings.HasPrefix(errStr, "notification service:") {
									t.Errorf("sentinel error '%s' message '%s' must start with prefix 'notification service:'", name.Name, errStr)
								}
							}
						}
					}
				}
			}
		}
	}
	if errorVarsFound == 0 {
		t.Error("expected at least one sentinel error declaration in domain/errors.go")
	}
}
