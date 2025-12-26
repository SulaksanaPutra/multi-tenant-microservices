package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/rabbitmq"
	"user-service/internal/service"
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

type mockUserService struct {
	createUserFromWorkspaceFunc func(ctx context.Context, input service.CreateUserFromWorkspaceInput) error
}

func (m *mockUserService) CreateUserFromWorkspace(ctx context.Context, input service.CreateUserFromWorkspaceInput) error {
	if m.createUserFromWorkspaceFunc != nil {
		return m.createUserFromWorkspaceFunc(ctx, input)
	}
	return nil
}

type mockInboxRepository struct {
	tryInsertFunc func(ctx context.Context, eventID string) (bool, error)
}

func (m *mockInboxRepository) TryInsert(ctx context.Context, eventID string) (bool, error) {
	if m.tryInsertFunc != nil {
		return m.tryInsertFunc(ctx, eventID)
	}
	return false, nil
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

func TestWorkspaceInitiatedConsumer_HandleDelivery(t *testing.T) {
	validEvt := domain.WorkspaceInitiatedEvent{
		EventID:    "evt-100",
		TenantID:   "tenant-abc",
		OwnerEmail: "owner@company.com",
		OwnerName:  "Alice Developer",
	}
	validBody, _ := json.Marshal(validEvt)

	t.Run("success_new_event", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxRepo := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				return false, nil // not duplicate
			},
		}

		var capturedInput service.CreateUserFromWorkspaceInput
		userSvc := &mockUserService{
			createUserFromWorkspaceFunc: func(ctx context.Context, input service.CreateUserFromWorkspaceInput) error {
				capturedInput = input
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			txManager:       txManager,
			inboxRepository: inboxRepo,
			userService:     userSvc,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		err := c.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected message to be ACKed")
		}
		if capturedInput.EventID != "evt-100" || capturedInput.TenantID != "tenant-abc" || capturedInput.OwnerEmail != "owner@company.com" {
			t.Errorf("unexpected input passed to service: %+v", capturedInput)
		}
	})

	t.Run("duplicate_event_skips_service", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxRepo := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				return true, nil // duplicate
			},
		}

		serviceCalled := false
		userSvc := &mockUserService{
			createUserFromWorkspaceFunc: func(ctx context.Context, input service.CreateUserFromWorkspaceInput) error {
				serviceCalled = true
				return nil
			},
		}

		c := &WorkspaceInitiatedConsumer{
			txManager:       txManager,
			inboxRepository: inboxRepo,
			userService:     userSvc,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		err := c.handleDelivery(context.Background(), d)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !mockAck.ackCalled {
			t.Error("expected duplicate event to be ACKed")
		}
		if serviceCalled {
			t.Error("expected user service NOT to be called on duplicate event")
		}
	})

	t.Run("invalid_json_nacks_without_requeue", func(t *testing.T) {
		c := &WorkspaceInitiatedConsumer{}
		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         []byte("invalid-json-{"),
		}

		err := c.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected json unmarshal error")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed")
		}
		if mockAck.requeueVal {
			t.Error("expected requeue=false for bad JSON payload (poison pill DLQ)")
		}
	})

	t.Run("inbox_error_nacks_with_requeue", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxErr := errors.New("db connection timeout")
		inboxRepo := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				return false, inboxErr
			},
		}

		c := &WorkspaceInitiatedConsumer{
			txManager:       txManager,
			inboxRepository: inboxRepo,
			userService:     &mockUserService{},
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		err := c.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected error when inbox guard fails")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on inbox error")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for transient inbox DB error")
		}
	})

	t.Run("service_error_nacks_with_requeue", func(t *testing.T) {
		txManager := &mockTxManager{}
		inboxRepo := &mockInboxRepository{
			tryInsertFunc: func(ctx context.Context, eventID string) (bool, error) {
				return false, nil
			},
		}

		svcErr := errors.New("failed to create user profile")
		userSvc := &mockUserService{
			createUserFromWorkspaceFunc: func(ctx context.Context, input service.CreateUserFromWorkspaceInput) error {
				return svcErr
			},
		}

		c := &WorkspaceInitiatedConsumer{
			txManager:       txManager,
			inboxRepository: inboxRepo,
			userService:     userSvc,
		}

		mockAck := &mockAcknowledger{}
		d := rabbitmq.Delivery{
			Acknowledger: mockAck,
			Body:         validBody,
		}

		err := c.handleDelivery(context.Background(), d)
		if err == nil {
			t.Error("expected error when user service fails")
		}
		if !mockAck.nackCalled {
			t.Error("expected message to be NACKed on service error")
		}
		if !mockAck.requeueVal {
			t.Error("expected requeue=true for transient service error")
		}
	})
}
