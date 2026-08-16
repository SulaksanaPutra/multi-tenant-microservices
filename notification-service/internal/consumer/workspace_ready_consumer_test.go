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

func newWorkspaceReadyConsumer(txManager TxManager, inboxService InboxService, notificationService NotificationService, authClient AuthClient, mailer Mailer) *WorkspaceReadyConsumer {
	if authClient == nil {
		authClient = &mockAuthClient{}
	}
	return &WorkspaceReadyConsumer{
		txManager:           txManager,
		inboxService:        inboxService,
		notificationService: notificationService,
		authClient:          authClient,
		mailer:              mailer,
	}
}

func makeWorkspaceReadyBody(t *testing.T) []byte {
	t.Helper()
	b, _ := json.Marshal(domain.WorkspaceReadyEvent{
		EventID:    "evt-ws-1",
		TenantID:   "tenant-88",
		OwnerEmail: "owner@company.com",
	})
	return b
}

func TestWorkspaceReadyConsumer_HandleDelivery_Success(t *testing.T) {
	body := makeWorkspaceReadyBody(t)

	var capturedInput service.ProcessEventInput
	statusUpdated := false
	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			capturedInput = input
			return &service.ProcessEventOutput{LogID: "ntf_1", RecipientEmail: "owner@company.com", TenantID: "tenant-88"}, nil
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

	workspaceReadyConsumer := newWorkspaceReadyConsumer(&mockTxManager{}, &mockInboxService{}, mockNotificationService, nil, mailer)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := workspaceReadyConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK")
	}
	if !emailSent {
		t.Error("expected email to be dispatched after transaction commits")
	}
	if !statusUpdated {
		t.Error("expected notification status to be updated to 'sent'")
	}
	if capturedInput.EventID != "evt-ws-1" || capturedInput.TenantID != "tenant-88" || capturedInput.OwnerEmail != "owner@company.com" {
		t.Errorf("unexpected input passed to service: %+v", capturedInput)
	}
}

func TestWorkspaceReadyConsumer_HandleDelivery_InvalidJSON(t *testing.T) {
	workspaceReadyConsumer := newWorkspaceReadyConsumer(nil, nil, nil, nil, nil)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: []byte("invalid-json")}

	err := workspaceReadyConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected json unmarshal error")
	}
	if !mockAck.nackCalled || mockAck.requeueVal {
		t.Error("expected NACK with requeue=false for bad JSON")
	}
}

func TestWorkspaceReadyConsumer_HandleDelivery_ServiceError_Nacks(t *testing.T) {
	body := makeWorkspaceReadyBody(t)
	serviceErr := errors.New("mail sending failed")

	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			return nil, serviceErr
		},
	}

	workspaceReadyConsumer := newWorkspaceReadyConsumer(&mockTxManager{}, &mockInboxService{}, mockNotificationService, nil, &mockMailer{})
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := workspaceReadyConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error when service fails")
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true")
	}
}

func TestWorkspaceReadyConsumer_HandleDelivery_SMTPError_Nacks(t *testing.T) {
	body := makeWorkspaceReadyBody(t)
	smtpErr := errors.New("smtp timeout")

	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			return &service.ProcessEventOutput{LogID: "ntf_2", UserID: "usr_200", RecipientEmail: "owner@company.com", TenantID: "tenant-88"}, nil
		},
	}
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error) {
			return "", "", smtpErr
		},
	}

	workspaceReadyConsumer := newWorkspaceReadyConsumer(&mockTxManager{}, &mockInboxService{}, mockNotificationService, nil, mailer)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := workspaceReadyConsumer.handleDelivery(context.Background(), d)
	if !errors.Is(err, smtpErr) {
		t.Errorf("expected smtpErr, got %v", err)
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true for SMTP failure")
	}
}

func TestWorkspaceReadyConsumer_HandleDelivery_NoEmailWhenBarrierNotMet(t *testing.T) {
	body := makeWorkspaceReadyBody(t)

	mockNotificationService := &mockNotificationService{
		processEventAndTrySendWelcomeFunc: func(ctx context.Context, input service.ProcessEventInput, events []domain.InboxMessage) (*service.ProcessEventOutput, error) {
			return nil, nil // barrier not yet met
		},
	}

	emailSent := false
	mailer := &mockMailer{
		sendWelcomeEmailFunc: func(recipientEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error) {
			emailSent = true
			return "", "", nil
		},
	}

	workspaceReadyConsumer := newWorkspaceReadyConsumer(&mockTxManager{}, &mockInboxService{}, mockNotificationService, nil, mailer)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := workspaceReadyConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if emailSent {
		t.Error("expected NO email when barrier is not met")
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK when barrier not met (not an error condition)")
	}
}

func TestWorkspaceReadyConsumer_HandleDelivery_MisroutedRoutingKey_AcksAndDiscards(t *testing.T) {
	// Simulates a ghost AMQP binding delivering a user.created message to
	// the notification_service_workspace_ready queue. The routing key guard must
	// discard silently with Ack — no inbox write, no notification service call.
	body, _ := json.Marshal(domain.UserCreatedEvent{
		EventID:  "evt-misrouted-2",
		UserID:   "usr_999",
		TenantID: "tenant-77",
		Email:    "user@example.com",
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

	workspaceReadyConsumer := newWorkspaceReadyConsumer(&mockTxManager{}, inboxService, mockNotificationService, nil, &mockMailer{})
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{
		Acknowledger: mockAck,
		Body:         body,
		RoutingKey:   domain.RoutingKeyUserCreated, // misrouted!
	}

	err := workspaceReadyConsumer.handleDelivery(context.Background(), d)
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

func TestWorkspaceReadyConsumer_HandleDelivery_DuplicateInbox_AlreadySent_Acks(t *testing.T) {
	body := makeWorkspaceReadyBody(t)

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

	workspaceReadyConsumer := newWorkspaceReadyConsumer(&mockTxManager{}, inboxService, mockNotificationService, nil, &mockMailer{})
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := workspaceReadyConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error on duplicate, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK on duplicate when already sent (idempotent skip)")
	}
}

func TestWorkspaceReadyConsumer_HandleDelivery_DuplicateInbox_Pending_RetriesAndSends(t *testing.T) {
	body := makeWorkspaceReadyBody(t)

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
			return &service.ProcessEventOutput{LogID: "ntf_retry_2", RecipientEmail: "owner@company.com", TenantID: "tenant-88"}, nil
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

	workspaceReadyConsumer := newWorkspaceReadyConsumer(&mockTxManager{}, inboxService, mockNotificationService, nil, mailer)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := workspaceReadyConsumer.handleDelivery(context.Background(), d)
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
