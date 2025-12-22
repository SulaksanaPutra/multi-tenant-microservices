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
)

type mockNotificationRepo struct {
	createNotificationLogFunc func(ctx context.Context, log domain.NotificationLog) (int, error)
	hasSentNotificationFunc   func(ctx context.Context, tenantID string) (bool, error)
	listNotificationsFunc     func(ctx context.Context, tenantID string) ([]domain.NotificationLog, error)
}

func (m *mockNotificationRepo) CreateNotificationLog(ctx context.Context, log domain.NotificationLog) (int, error) {
	if m.createNotificationLogFunc != nil {
		return m.createNotificationLogFunc(ctx, log)
	}
	return 1, nil
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

type mockInboxRepo struct {
	tryInsertFunc           func(ctx context.Context, msg domain.InboxMessage) (bool, error)
	getEventsByTenantIDFunc func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error)
}

func (m *mockInboxRepo) TryInsert(ctx context.Context, msg domain.InboxMessage) (bool, error) {
	if m.tryInsertFunc != nil {
		return m.tryInsertFunc(ctx, msg)
	}
	return false, nil
}

func (m *mockInboxRepo) GetEventsByTenantID(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
	if m.getEventsByTenantIDFunc != nil {
		return m.getEventsByTenantIDFunc(ctx, tenantID)
	}
	return nil, nil
}

type mockMailer struct {
	sendWelcomeEmailFunc func(recipientEmail, tenantID string) (string, string, error)
}

func (m *mockMailer) SendWelcomeEmail(recipientEmail, tenantID string) (string, string, error) {
	if m.sendWelcomeEmailFunc != nil {
		return m.sendWelcomeEmailFunc(recipientEmail, tenantID)
	}
	return "Welcome", "Body", nil
}

func TestProcessEventAndTrySendWelcome_Validation(t *testing.T) {
	svc := NewNotificationService(&mockNotificationRepo{}, &mockInboxRepo{}, &mockMailer{})

	t.Run("missing event_id", func(t *testing.T) {
		err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
			TenantID: "tenant-1",
		})
		if !errors.Is(err, ErrEventIDRequired) {
			t.Errorf("expected ErrEventIDRequired, got %v", err)
		}
	})

	t.Run("missing tenant_id", func(t *testing.T) {
		err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
			EventID: "evt-1",
		})
		if !errors.Is(err, ErrTenantIDRequired) {
			t.Errorf("expected ErrTenantIDRequired, got %v", err)
		}
	})
}

func TestProcessEventAndTrySendWelcome_DuplicateInbox(t *testing.T) {
	inboxRepo := &mockInboxRepo{
		tryInsertFunc: func(ctx context.Context, msg domain.InboxMessage) (bool, error) {
			return true, nil // duplicate event
		},
	}

	svc := NewNotificationService(&mockNotificationRepo{}, inboxRepo, &mockMailer{})

	err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "evt-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("expected no error on duplicate, got %v", err)
	}
}

func TestProcessEventAndTrySendWelcome_WaitingBarrierCondition(t *testing.T) {
	// Only user.created present, workspace.ready missing
	userEvtBytes, _ := json.Marshal(map[string]string{
		"user_id": "usr_123",
		"email":   "owner@company.com",
	})

	inboxRepo := &mockInboxRepo{
		getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
			return []domain.InboxMessage{
				{EventType: "user.created", Payload: userEvtBytes},
			}, nil
		},
	}

	emailSent := false
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID string) (string, string, error) {
			emailSent = true
			return "", "", nil
		},
	}

	svc := NewNotificationService(&mockNotificationRepo{}, inboxRepo, mailer)

	err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
		EventID:   "evt-1",
		TenantID:  "tenant-1",
		EventType: "user.created",
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if emailSent {
		t.Error("expected welcome email NOT to be sent when workspace.ready is missing")
	}
}

func TestProcessEventAndTrySendWelcome_BarrierMet_SendEmail(t *testing.T) {
	userEvtBytes, _ := json.Marshal(map[string]string{
		"user_id": "usr_123",
		"email":   "owner@company.com",
	})
	wsEvtBytes, _ := json.Marshal(map[string]string{
		"owner_email": "owner@company.com",
	})

	inboxRepo := &mockInboxRepo{
		getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
			return []domain.InboxMessage{
				{EventType: "user.created", Payload: userEvtBytes},
				{EventType: "workspace.ready", Payload: wsEvtBytes},
			}, nil
		},
	}

	emailSent := false
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID string) (string, string, error) {
			emailSent = true
			return "Welcome", "Body", nil
		},
	}

	svc := NewNotificationService(&mockNotificationRepo{}, inboxRepo, mailer)

	err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{
		EventID:   "evt-2",
		TenantID:  "tenant-1",
		EventType: "workspace.ready",
	})

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !emailSent {
		t.Error("expected welcome email to be sent when both barrier events are present")
	}
}

func TestProcessEventAndTrySendWelcome_AlreadySent(t *testing.T) {
	userEvtBytes, _ := json.Marshal(map[string]string{"user_id": "usr_123", "email": "a@b.com"})
	wsEvtBytes, _ := json.Marshal(map[string]string{"owner_email": "a@b.com"})

	inboxRepo := &mockInboxRepo{
		getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
			return []domain.InboxMessage{
				{EventType: "user.created", Payload: userEvtBytes},
				{EventType: "workspace.ready", Payload: wsEvtBytes},
			}, nil
		},
	}

	notifRepo := &mockNotificationRepo{
		hasSentNotificationFunc: func(ctx context.Context, tenantID string) (bool, error) {
			return true, nil // already sent
		},
	}

	emailSent := false
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID string) (string, string, error) {
			emailSent = true
			return "", "", nil
		},
	}

	svc := NewNotificationService(notifRepo, inboxRepo, mailer)

	err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "e-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if emailSent {
		t.Error("expected email NOT to be re-sent if alreadySent is true")
	}
}

func TestProcessEventAndTrySendWelcome_Errors(t *testing.T) {
	expectedErr := errors.New("infra failure")

	t.Run("TryInsert error", func(t *testing.T) {
		inboxRepo := &mockInboxRepo{
			tryInsertFunc: func(ctx context.Context, msg domain.InboxMessage) (bool, error) {
				return false, expectedErr
			},
		}
		svc := NewNotificationService(&mockNotificationRepo{}, inboxRepo, &mockMailer{})
		err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "e-1", TenantID: "t-1"})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("GetEventsByTenantID error", func(t *testing.T) {
		inboxRepo := &mockInboxRepo{
			getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
				return nil, expectedErr
			},
		}
		svc := NewNotificationService(&mockNotificationRepo{}, inboxRepo, &mockMailer{})
		err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "e-1", TenantID: "t-1"})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("HasSentNotification error", func(t *testing.T) {
		inboxRepo := &mockInboxRepo{
			getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
				return []domain.InboxMessage{
					{EventType: "user.created"},
					{EventType: "workspace.ready"},
				}, nil
			},
		}
		notifRepo := &mockNotificationRepo{
			hasSentNotificationFunc: func(ctx context.Context, tenantID string) (bool, error) {
				return false, expectedErr
			},
		}
		svc := NewNotificationService(notifRepo, inboxRepo, &mockMailer{})
		err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "e-1", TenantID: "t-1"})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("CreateNotificationLog error", func(t *testing.T) {
		inboxRepo := &mockInboxRepo{
			getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
				return []domain.InboxMessage{
					{EventType: "user.created"},
					{EventType: "workspace.ready"},
				}, nil
			},
		}
		notifRepo := &mockNotificationRepo{
			createNotificationLogFunc: func(ctx context.Context, log domain.NotificationLog) (int, error) {
				return 0, expectedErr
			},
		}
		svc := NewNotificationService(notifRepo, inboxRepo, &mockMailer{})
		err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "e-1", TenantID: "t-1"})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("SendWelcomeEmail error", func(t *testing.T) {
		inboxRepo := &mockInboxRepo{
			getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
				return []domain.InboxMessage{
					{EventType: "user.created"},
					{EventType: "workspace.ready"},
				}, nil
			},
		}
		mailer := &mockMailer{
			sendWelcomeEmailFunc: func(recipientEmail, tenantID string) (string, string, error) {
				return "", "", expectedErr
			},
		}
		svc := NewNotificationService(&mockNotificationRepo{}, inboxRepo, mailer)
		err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "e-1", TenantID: "t-1"})
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestProcessEventAndTrySendWelcome_PayloadFallback(t *testing.T) {
	// Corrupt JSON payload should trigger fallback values from ProcessEventInput or defaults
	inboxRepo := &mockInboxRepo{
		getEventsByTenantIDFunc: func(ctx context.Context, tenantID string) ([]domain.InboxMessage, error) {
			return []domain.InboxMessage{
				{EventType: "user.created", Payload: []byte("invalid-json")},
				{EventType: "workspace.ready", Payload: []byte("{invalid}")},
			}, nil
		},
	}

	var capturedLog domain.NotificationLog
	notifRepo := &mockNotificationRepo{
		createNotificationLogFunc: func(ctx context.Context, log domain.NotificationLog) (int, error) {
			capturedLog = log
			return 1, nil
		},
	}

	svc := NewNotificationService(notifRepo, inboxRepo, &mockMailer{})

	input := ProcessEventInput{
		EventID:    "evt-fallback",
		TenantID:   "t-fallback",
		UserID:     "usr_explicit",
		OwnerEmail: "explicit@domain.com",
	}

	err := svc.ProcessEventAndTrySendWelcome(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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
		svc := NewNotificationService(notifRepo, &mockInboxRepo{}, &mockMailer{})

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
		svc := NewNotificationService(notifRepo, &mockInboxRepo{}, &mockMailer{})

		_, err := svc.ListNotifications(context.Background(), "t-1")
		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})
}

func TestNotificationService_ErrorContractInvariants(t *testing.T) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, "notification_service.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse notification_service.go AST: %v", err)
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
								if !strings.HasPrefix(errStr, "notification service:") {
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
		t.Error("expected at least one sentinel error declaration in notification_service.go")
	}
}
