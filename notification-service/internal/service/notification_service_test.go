package service

import (
	"context"
	"encoding/json"
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

func TestProcessEventAndTrySendWelcome_DuplicateInbox(t *testing.T) {
	inboxRepo := &mockInboxRepo{
		tryInsertFunc: func(ctx context.Context, msg domain.InboxMessage) (bool, error) {
			return true, nil // duplicate event
		},
	}

	svc := NewNotificationService(&mockNotificationRepo{}, inboxRepo, &mockMailer{})

	err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{EventID: "evt-1"})
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

	err := svc.ProcessEventAndTrySendWelcome(context.Background(), ProcessEventInput{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if emailSent {
		t.Error("expected email NOT to be re-sent if alreadySent is true")
	}
}
