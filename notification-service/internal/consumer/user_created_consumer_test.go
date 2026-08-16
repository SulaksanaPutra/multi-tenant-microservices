package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"notification-service/internal/domain"
	"notification-service/internal/infrastructure/rabbitmq"
	"notification-service/internal/service"
)

type mockTxManager struct {
	withTransactionFunc func(ctx context.Context, fn func(txCtx context.Context) error) error
}

func (m *mockTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	if m.withTransactionFunc != nil {
		return m.withTransactionFunc(ctx, fn)
	}
	return fn(ctx)
}

type mockNotificationService struct {
	processEventAndTrySendWelcomeFunc func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error)
	updateNotificationStatusFunc      func(ctx context.Context, logID string, status string) error
	hasSentNotificationFunc           func(ctx context.Context, tenantID string) (bool, error)
	createOrderNotificationFunc       func(ctx context.Context, evt domain.OrderCreatedEvent) error
}

func (m *mockNotificationService) ProcessEventAndTrySendWelcome(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
	if m.processEventAndTrySendWelcomeFunc != nil {
		return m.processEventAndTrySendWelcomeFunc(ctx, input, events)
	}
	return nil, nil
}

func (m *mockNotificationService) UpdateNotificationStatus(ctx context.Context, logID string, status string) error {
	if m.updateNotificationStatusFunc != nil {
		return m.updateNotificationStatusFunc(ctx, logID, status)
	}
	return nil
}

func (m *mockNotificationService) HasSentNotification(ctx context.Context, tenantID string) (bool, error) {
	if m.hasSentNotificationFunc != nil {
		return m.hasSentNotificationFunc(ctx, tenantID)
	}
	return false, nil
}

func (m *mockNotificationService) CreateOrderNotification(ctx context.Context, evt domain.OrderCreatedEvent) error {
	if m.createOrderNotificationFunc != nil {
		return m.createOrderNotificationFunc(ctx, evt)
	}
	return nil
}

type mockInboxService struct {
	claimEventFunc        func(txCtx context.Context, input service.ClaimInboxInput) (bool, error)
	listBarrierEventsFunc func(txCtx context.Context, tenantID string) ([]domain.InboxMessage, error)
}

func (m *mockInboxService) ClaimEvent(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
	if m.claimEventFunc != nil {
		return m.claimEventFunc(txCtx, input)
	}
	return false, nil
}

func (m *mockInboxService) ListBarrierEvents(txCtx context.Context, tenantID string) ([]domain.InboxMessage, error) {
	if m.listBarrierEventsFunc != nil {
		return m.listBarrierEventsFunc(txCtx, tenantID)
	}
	return []domain.InboxMessage{}, nil
}

type mockAuthClient struct {
	fetchSetupTokenFunc func(ctx context.Context, userID, tenantID, email string) (string, error)
}

func (m *mockAuthClient) FetchSetupToken(ctx context.Context, userID, tenantID, email string) (string, error) {
	if m.fetchSetupTokenFunc != nil {
		return m.fetchSetupTokenFunc(ctx, userID, tenantID, email)
	}
	return "mock_setup_token", nil
}

type mockMailer struct {
	sendWelcomeEmailFunc func(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error)
}

func (m *mockMailer) SendWelcomeEmail(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error) {
	if m.sendWelcomeEmailFunc != nil {
		return m.sendWelcomeEmailFunc(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken)
	}
	return "Welcome", "Body", nil
}

type mockAcknowledger struct {
	ackCalled   bool
	nackCalled  bool
	requeueVal  bool
	multipleVal bool
}

func (m *mockAcknowledger) Ack(tag uint64, multiple bool) error {
	m.ackCalled = true
	m.multipleVal = multiple
	return nil
}

func (m *mockAcknowledger) Nack(tag uint64, multiple, requeue bool) error {
	m.nackCalled = true
	m.multipleVal = multiple
	m.requeueVal = requeue
	return nil
}

func (m *mockAcknowledger) Reject(tag uint64, requeue bool) error {
	return nil
}

func newUserCreatedConsumer(txManager TxManager, inboxService InboxService, notificationService NotificationService, authClient AuthClient, mailer Mailer) *UserCreatedConsumer {
	if authClient == nil {
		authClient = &mockAuthClient{}
	}
	return &UserCreatedConsumer{
		txManager:           txManager,
		inboxService:        inboxService,
		notificationService: notificationService,
		authClient:          authClient,
		mailer:              mailer,
	}
}

func makeUserCreatedBody(t *testing.T) []byte {
	t.Helper()
	b, _ := json.Marshal(domain.UserCreatedEvent{
		EventID:  "evt-user-1",
		UserID:   "usr_100",
		TenantID: "tenant-99",
		Email:    "john@example.com",
		Name:     "John Doe",
	})
	return b
}

func TestUserCreatedConsumer_HandleDelivery_Success(t *testing.T) {
	body := makeUserCreatedBody(t)

	var capturedInput service.ProcessEventInput
	statusUpdated := false
	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			capturedInput = input
			return &service.ProcessEventOutput{LogID: "ntf_1", UserID: "usr_100", RecipientEmail: "john@example.com", TenantID: "tenant-99"}, nil
		},
		updateNotificationStatusFunc: func(ctx context.Context, logID string, status string) error {
			if logID == "ntf_1" && status == "sent" {
				statusUpdated = true
			}
			return nil
		},
	}

	emailSent := false
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error) {
			emailSent = true
			return "subj", "body", nil
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, &mockInboxService{}, mockNotificationService, nil, mailer)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK")
	}
	if !emailSent {
		t.Error("expected email to be sent after transaction commits")
	}
	if !statusUpdated {
		t.Error("expected notification status to be updated to 'sent'")
	}
	if capturedInput.EventID != "evt-user-1" || capturedInput.UserID != "usr_100" || capturedInput.OwnerEmail != "john@example.com" {
		t.Errorf("unexpected input passed to service: %+v", capturedInput)
	}
}

func TestUserCreatedConsumer_HandleDelivery_InvalidJSON(t *testing.T) {
	userCreatedConsumer := newUserCreatedConsumer(nil, nil, nil, nil, nil)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: []byte("invalid-json")}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected unmarshal error")
	}
	if !mockAck.nackCalled || mockAck.requeueVal {
		t.Error("expected NACK with requeue=false for bad JSON")
	}
}

func TestUserCreatedConsumer_HandleDelivery_DuplicateInbox_AlreadySent_Acks(t *testing.T) {
	body := makeUserCreatedBody(t)

	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			return true, nil // duplicate
		},
	}
	mockNotificationService := &mockNotificationService{
		hasSentNotificationFunc: func(ctx context.Context, tenantID string) (bool, error) {
			return true, nil // already sent
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, mockNotificationService, nil, &mockMailer{})
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error on duplicate, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK on duplicate when already sent (idempotent skip)")
	}
}

func TestUserCreatedConsumer_HandleDelivery_DuplicateInbox_Pending_RetriesAndSends(t *testing.T) {
	body := makeUserCreatedBody(t)

	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			return true, nil // duplicate event_id
		},
	}
	emailSent := false
	mockNotificationService := &mockNotificationService{
		hasSentNotificationFunc: func(ctx context.Context, tenantID string) (bool, error) {
			return false, nil // NOT yet sent!
		},
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			return &service.ProcessEventOutput{LogID: "ntf_retry_1", UserID: "usr_100", RecipientEmail: "john@example.com", TenantID: "tenant-99"}, nil
		},
		updateNotificationStatusFunc: func(ctx context.Context, logID string, status string) error {
			return nil
		},
	}
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error) {
			emailSent = true
			return "Welcome", "Body", nil
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, mockNotificationService, nil, mailer)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error on duplicate retry, got %v", err)
	}
	if !emailSent {
		t.Error("expected email to be dispatched during retry even if isDup is true")
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK after successful email dispatch")
	}
}

func TestUserCreatedConsumer_HandleDelivery_InboxClaimError_Nacks(t *testing.T) {
	body := makeUserCreatedBody(t)
	inboxErr := errors.New("db connection lost")

	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			return false, inboxErr
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, &mockNotificationService{}, nil, &mockMailer{})
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error from inbox claim failure")
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true for transient inbox error")
	}
}

func TestUserCreatedConsumer_HandleDelivery_SMTPError_Nacks(t *testing.T) {
	body := makeUserCreatedBody(t)
	smtpErr := errors.New("smtp timeout")

	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			return &service.ProcessEventOutput{LogID: "ntf_1", UserID: "usr_100", RecipientEmail: "john@example.com", TenantID: "tenant-99"}, nil
		},
	}
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error) {
			return "", "", smtpErr
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, &mockInboxService{}, mockNotificationService, nil, mailer)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if !errors.Is(err, smtpErr) {
		t.Errorf("expected smtpErr, got %v", err)
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true for transient SMTP error")
	}
}

func TestUserCreatedConsumer_HandleDelivery_ServiceError_Nacks(t *testing.T) {
	body := makeUserCreatedBody(t)
	serviceErr := errors.New("service failure")

	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			return nil, serviceErr
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, &mockInboxService{}, mockNotificationService, nil, &mockMailer{})
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error when service fails")
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true for transient service error")
	}
}

func TestUserCreatedConsumer_HandleDelivery_NoEmailWhenBarrierNotMet(t *testing.T) {
	body := makeUserCreatedBody(t)

	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			return nil, nil // barrier not met
		},
	}

	emailSent := false
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error) {
			emailSent = true
			return "", "", nil
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, &mockInboxService{}, mockNotificationService, nil, mailer)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if emailSent {
		t.Error("expected NO email when barrier is not met")
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK when barrier not met (not an error)")
	}
}

func TestUserCreatedConsumer_HandleDelivery_MisroutedRoutingKey_AcksAndDiscards(t *testing.T) {
	// Simulates a ghost AMQP binding delivering a workspace.ready message to
	// the notification_service_user_created queue. The routing key guard must
	// discard silently with Ack.
	body, _ := json.Marshal(domain.WorkspaceReadyEvent{
		EventID:    "evt-misrouted-1",
		TenantID:   "tenant-99",
		OwnerEmail: "owner@example.com",
	})

	inboxCalled := false
	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			inboxCalled = true
			return false, nil
		},
	}

	notificationCalled := false
	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			notificationCalled = true
			return nil, nil
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, mockNotificationService, nil, &mockMailer{})
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{
		Acknowledger: mockAck,
		Body:         body,
		RoutingKey:   domain.RoutingKeyWorkspaceReady, // misrouted!
	}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error for misrouted message, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK to drain misrouted message from queue")
	}
	if mockAck.nackCalled {
		t.Error("expected no NACK for misrouted message")
	}
	if inboxCalled {
		t.Error("expected inbox service NOT to be called for misrouted message")
	}
	if notificationCalled {
		t.Error("expected notification service NOT to be called for misrouted message")
	}
}
