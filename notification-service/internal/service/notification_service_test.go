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
	createNotificationLogFunc    func(ctx context.Context, input repository.CreateNotificationLogInput) (int, error)
	updateNotificationStatusFunc func(ctx context.Context, id int, status string) error
	hasSentNotificationFunc      func(ctx context.Context, tenantID string) (bool, error)
	listNotificationsFunc        func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error)
}

func (m *mockNotificationRepo) CreateNotificationLog(ctx context.Context, input repository.CreateNotificationLogInput) (int, error) {
	if m.createNotificationLogFunc != nil {
		return m.createNotificationLogFunc(ctx, input)
	}
	return 1, nil
}

func (m *mockNotificationRepo) UpdateNotificationStatus(ctx context.Context, id int, status string) error {
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
	b, _ := json.Marshal(map[string]string{"owner_email": ownerEmail})
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
		createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (int, error) {
			capturedLog = input
			return 42, nil
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
	if details.LogID != 42 {
		t.Errorf("expected LogID=42, got %d", details.LogID)
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

	t.Run("CreateNotificationLog error", func(t *testing.T) {
		notifRepo := &mockNotificationRepo{
			createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (int, error) {
				return 0, expectedErr
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

func TestProcessEventAndTrySendWelcome_PayloadFallback(t *testing.T) {
	// Corrupt JSON payloads should trigger fallback to ProcessEventInput values.
	events := []domain.InboxMessage{
		{EventType: "user.created", Payload: []byte("invalid-json")},
		{EventType: "workspace.ready", Payload: []byte("{invalid}")},
	}

	var capturedLog repository.CreateNotificationLogInput
	notifRepo := &mockNotificationRepo{
		createNotificationLogFunc: func(ctx context.Context, input repository.CreateNotificationLogInput) (int, error) {
			capturedLog = input
			return 1, nil
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
	if capturedLog.UserID != "usr_explicit" || capturedLog.RecipientEmail != "explicit@domain.com" {
		t.Errorf("expected fallback to input values, got %+v", capturedLog)
	}
}

func TestNotificationService_ListNotifications(t *testing.T) {
	t.Run("returns notifications list", func(t *testing.T) {
		expectedLogs := []domain.NotificationLog{
			{ID: 1, TenantID: "t-1", RecipientEmail: "a@b.com"},
		}
		notifRepo := &mockNotificationRepo{
			listNotificationsFunc: func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
				return expectedLogs, nil
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
