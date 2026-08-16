package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/rabbitmq"
	"auth-service/internal/service"
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

type mockInboxService struct {
	claimEventFunc func(txCtx context.Context, input service.ClaimInboxInput) (bool, error)
}

func (m *mockInboxService) ClaimEvent(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
	if m.claimEventFunc != nil {
		return m.claimEventFunc(txCtx, input)
	}
	return false, nil
}

type mockMembershipService struct {
	addMembershipFunc func(ctx context.Context, userID, tenantID string) error
	calls             int
}

func (m *mockMembershipService) AddMembership(ctx context.Context, userID, tenantID string) error {
	m.calls++
	if m.addMembershipFunc != nil {
		return m.addMembershipFunc(ctx, userID, tenantID)
	}
	return nil
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

func newUserCreatedConsumer(txManager TxManager, inboxService InboxService, membershipService MembershipService, maxDeliveries int) *UserCreatedConsumer {
	// 0 means "no cap interference" for tests; real cap logic is exercised
	// explicitly in the max-deliveries tests.
	if maxDeliveries <= 0 {
		maxDeliveries = 1000
	}
	return &UserCreatedConsumer{
		txManager:         txManager,
		inboxService:      inboxService,
		membershipService: membershipService,
		maxDeliveries:     maxDeliveries,
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

	var capturedInput service.ClaimInboxInput
	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			capturedInput = input
			return false, nil
		},
	}

	membershipService := &mockMembershipService{}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, membershipService, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK")
	}
	if membershipService.calls != 1 {
		t.Errorf("expected membership upsert called once, got %d", membershipService.calls)
	}
	if capturedInput.EventID != "evt-user-1" || capturedInput.TenantID != "tenant-99" || capturedInput.EventType != domain.RoutingKeyUserCreated {
		t.Errorf("unexpected inbox input: %+v", capturedInput)
	}
}

func TestUserCreatedConsumer_HandleDelivery_InvalidJSON(t *testing.T) {
	userCreatedConsumer := newUserCreatedConsumer(nil, nil, nil, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: []byte("invalid-json")}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected unmarshal error")
	}
	if !mockAck.nackCalled || mockAck.requeueVal {
		t.Error("expected NACK with requeue=false for bad JSON (poison -> DLX)")
	}
}

func TestUserCreatedConsumer_HandleDelivery_DuplicateInbox_Acks(t *testing.T) {
	body := makeUserCreatedBody(t)

	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			return true, nil // duplicate — skip cleanly
		},
	}

	membershipService := &mockMembershipService{}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, membershipService, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error on duplicate, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK even on duplicate (idempotent skip)")
	}
	if membershipService.calls != 0 {
		t.Errorf("expected no membership upsert on duplicate, got %d calls", membershipService.calls)
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

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, &mockMembershipService{}, 0)
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

func TestUserCreatedConsumer_HandleDelivery_MembershipError_Nacks(t *testing.T) {
	body := makeUserCreatedBody(t)
	serviceErr := errors.New("membership upsert failed")

	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			return false, nil
		},
	}
	membershipService := &mockMembershipService{
		addMembershipFunc: func(ctx context.Context, userID, tenantID string) error {
			return serviceErr
		},
	}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, membershipService, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error when membership upsert fails")
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true for transient membership error")
	}
}

func TestUserCreatedConsumer_HandleDelivery_MaxDeliveries_RoutesToDLQ(t *testing.T) {
	body := makeUserCreatedBody(t)

	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			return false, nil
		},
	}
	membershipService := &mockMembershipService{
		addMembershipFunc: func(ctx context.Context, userID, tenantID string) error {
			return errors.New("persistent failure")
		},
	}

	// maxDeliveries = 3; this is the 3rd delivery -> must Nack(false,false) to DLX.
	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, membershipService, 3)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{
		Acknowledger: mockAck,
		Body:         body,
		Headers: map[string]interface{}{
			"x-delivery-count": int64(3),
		},
	}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error when max deliveries reached")
	}
	if !mockAck.nackCalled || mockAck.requeueVal {
		t.Error("expected NACK with requeue=false (broker routes to DLX) at max delivery count")
	}
}

func TestUserCreatedConsumer_HandleDelivery_BelowMaxDeliveries_Requeues(t *testing.T) {
	body := makeUserCreatedBody(t)

	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			return false, nil
		},
	}
	membershipService := &mockMembershipService{
		addMembershipFunc: func(ctx context.Context, userID, tenantID string) error {
			return errors.New("transient failure")
		},
	}

	// maxDeliveries = 3; delivery_count = 2 -> still below cap, requeue.
	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, membershipService, 3)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{
		Acknowledger: mockAck,
		Body:         body,
		Headers: map[string]interface{}{
			"x-delivery-count": int64(2),
		},
	}

	err := userCreatedConsumer.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error on transient membership failure")
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true below max delivery count")
	}
}

func TestUserCreatedConsumer_HandleDelivery_MisroutedRoutingKey_AcksAndDiscards(t *testing.T) {
	// Simulates a ghost AMQP binding delivering a workspace.ready message to the
	// auth_service_user_created_membership queue. The routing key guard must
	// discard silently with Ack — no inbox write, no membership upsert.
	body := makeUserCreatedBody(t)

	inboxCalled := false
	inboxService := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input service.ClaimInboxInput) (bool, error) {
			inboxCalled = true
			return false, nil
		},
	}

	membershipService := &mockMembershipService{}

	userCreatedConsumer := newUserCreatedConsumer(&mockTxManager{}, inboxService, membershipService, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{
		Acknowledger: mockAck,
		Body:         body,
		RoutingKey:   "workspace.ready", // misrouted!
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
	if membershipService.calls != 0 {
		t.Error("expected membership service NOT to be called for misrouted message")
	}
}

func TestGetDeliveryCount_Quorum(t *testing.T) {
	if got := getDeliveryCount(map[string]interface{}{"x-delivery-count": int64(4)}); got != 4 {
		t.Errorf("expected 4, got %d", got)
	}
	if got := getDeliveryCount(map[string]interface{}{"x-delivery-count": int(2)}); got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
}

func TestGetDeliveryCount_ClassicDLXFallback(t *testing.T) {
	headers := map[string]interface{}{
		"x-death": []interface{}{
			map[string]interface{}{
				"count": int64(3),
			},
		},
	}
	if got := getDeliveryCount(headers); got != 3 {
		t.Errorf("expected 3 (x-death fallback), got %d", got)
	}
}

func TestGetDeliveryCount_Empty(t *testing.T) {
	if got := getDeliveryCount(nil); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
	if got := getDeliveryCount(map[string]interface{}{}); got != 0 {
		t.Errorf("expected 0 for empty headers, got %d", got)
	}
}

func TestNewUserCreatedConsumer(t *testing.T) {
	userCreatedConsumer := NewUserCreatedConsumer(UserCreatedConsumerParams{
		TxManager:         &mockTxManager{},
		Client:            &mockAMQPInterfaceClient{},
		InboxService:      &mockInboxService{},
		MembershipService: &mockMembershipService{},
	})
	if userCreatedConsumer == nil {
		t.Fatal("expected non-nil consumer")
	}
	if userCreatedConsumer.maxDeliveries != domain.MaxAuthUserCreatedDeliveries {
		t.Errorf("expected default maxDeliveries %d, got %d", domain.MaxAuthUserCreatedDeliveries, userCreatedConsumer.maxDeliveries)
	}
}
