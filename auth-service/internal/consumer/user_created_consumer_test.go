package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/rabbitmq"
	"auth-service/internal/repository"
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
	claimEventFunc func(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error)
}

func (m *mockInboxService) ClaimEvent(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
	if m.claimEventFunc != nil {
		return m.claimEventFunc(txCtx, input)
	}
	return false, nil
}

type mockMembershipRepository struct {
	addMembershipFunc func(ctx context.Context, userID, tenantID string) error
	calls             int
}

func (m *mockMembershipRepository) AddMembership(ctx context.Context, userID, tenantID string) error {
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

func newUserCreatedConsumer(txm TxManager, inbox InboxService, membership MembershipRepository, maxDeliveries int) *UserCreatedConsumer {
	// 0 means "no cap interference" for tests; real cap logic is exercised
	// explicitly in the max-deliveries tests.
	if maxDeliveries <= 0 {
		maxDeliveries = 1000
	}
	return &UserCreatedConsumer{
		txManager:            txm,
		inboxService:         inbox,
		membershipRepository: membership,
		maxDeliveries:        maxDeliveries,
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

	var capturedInput repository.CreateInboxMessageInput
	inbox := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			capturedInput = input
			return false, nil
		},
	}

	membership := &mockMembershipRepository{}

	c := newUserCreatedConsumer(&mockTxManager{}, inbox, membership, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := c.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK")
	}
	if membership.calls != 1 {
		t.Errorf("expected membership upsert called once, got %d", membership.calls)
	}
	if capturedInput.EventID != "evt-user-1" || capturedInput.TenantID != "tenant-99" || capturedInput.EventType != domain.RoutingKeyUserCreated {
		t.Errorf("unexpected inbox input: %+v", capturedInput)
	}
}

func TestUserCreatedConsumer_HandleDelivery_InvalidJSON(t *testing.T) {
	c := newUserCreatedConsumer(nil, nil, nil, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: []byte("invalid-json")}

	err := c.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected unmarshal error")
	}
	if !mockAck.nackCalled || mockAck.requeueVal {
		t.Error("expected NACK with requeue=false for bad JSON (poison -> DLX)")
	}
}

func TestUserCreatedConsumer_HandleDelivery_DuplicateInbox_Acks(t *testing.T) {
	body := makeUserCreatedBody(t)

	inbox := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return true, nil // duplicate — skip cleanly
		},
	}

	membership := &mockMembershipRepository{}

	c := newUserCreatedConsumer(&mockTxManager{}, inbox, membership, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := c.handleDelivery(context.Background(), d)
	if err != nil {
		t.Fatalf("expected no error on duplicate, got %v", err)
	}
	if !mockAck.ackCalled {
		t.Error("expected ACK even on duplicate (idempotent skip)")
	}
	if membership.calls != 0 {
		t.Errorf("expected no membership upsert on duplicate, got %d calls", membership.calls)
	}
}

func TestUserCreatedConsumer_HandleDelivery_InboxClaimError_Nacks(t *testing.T) {
	body := makeUserCreatedBody(t)
	inboxErr := errors.New("db connection lost")

	inbox := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return false, inboxErr
		},
	}

	c := newUserCreatedConsumer(&mockTxManager{}, inbox, &mockMembershipRepository{}, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := c.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error from inbox claim failure")
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true for transient inbox error")
	}
}

func TestUserCreatedConsumer_HandleDelivery_MembershipError_Nacks(t *testing.T) {
	body := makeUserCreatedBody(t)
	svcErr := errors.New("membership upsert failed")

	inbox := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return false, nil
		},
	}
	membership := &mockMembershipRepository{
		addMembershipFunc: func(ctx context.Context, userID, tenantID string) error {
			return svcErr
		},
	}

	c := newUserCreatedConsumer(&mockTxManager{}, inbox, membership, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{Acknowledger: mockAck, Body: body}

	err := c.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error when membership upsert fails")
	}
	if !mockAck.nackCalled || !mockAck.requeueVal {
		t.Error("expected NACK with requeue=true for transient membership error")
	}
}

func TestUserCreatedConsumer_HandleDelivery_MaxDeliveries_RoutesToDLQ(t *testing.T) {
	body := makeUserCreatedBody(t)

	inbox := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return false, nil
		},
	}
	membership := &mockMembershipRepository{
		addMembershipFunc: func(ctx context.Context, userID, tenantID string) error {
			return errors.New("persistent failure")
		},
	}

	// maxDeliveries = 3; this is the 3rd delivery -> must Nack(false,false) to DLX.
	c := newUserCreatedConsumer(&mockTxManager{}, inbox, membership, 3)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{
		Acknowledger: mockAck,
		Body:         body,
		Headers: map[string]interface{}{
			"x-delivery-count": int64(3),
		},
	}

	err := c.handleDelivery(context.Background(), d)
	if err == nil {
		t.Error("expected error when max deliveries reached")
	}
	if !mockAck.nackCalled || mockAck.requeueVal {
		t.Error("expected NACK with requeue=false (broker routes to DLX) at max delivery count")
	}
}

func TestUserCreatedConsumer_HandleDelivery_BelowMaxDeliveries_Requeues(t *testing.T) {
	body := makeUserCreatedBody(t)

	inbox := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			return false, nil
		},
	}
	membership := &mockMembershipRepository{
		addMembershipFunc: func(ctx context.Context, userID, tenantID string) error {
			return errors.New("transient failure")
		},
	}

	// maxDeliveries = 3; delivery_count = 2 -> still below cap, requeue.
	c := newUserCreatedConsumer(&mockTxManager{}, inbox, membership, 3)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{
		Acknowledger: mockAck,
		Body:         body,
		Headers: map[string]interface{}{
			"x-delivery-count": int64(2),
		},
	}

	err := c.handleDelivery(context.Background(), d)
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
	inbox := &mockInboxService{
		claimEventFunc: func(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
			inboxCalled = true
			return false, nil
		},
	}

	membership := &mockMembershipRepository{}

	c := newUserCreatedConsumer(&mockTxManager{}, inbox, membership, 0)
	mockAck := &mockAcknowledger{}
	d := rabbitmq.Delivery{
		Acknowledger: mockAck,
		Body:         body,
		RoutingKey:   "workspace.ready", // misrouted!
	}

	err := c.handleDelivery(context.Background(), d)
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
	if membership.calls != 0 {
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
