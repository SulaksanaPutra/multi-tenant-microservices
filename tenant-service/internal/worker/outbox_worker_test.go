package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"tenant-service/internal/domain"
)

type mockOutboxRepository struct {
	mu                      sync.Mutex
	recoverStuckClaimsFunc  func(ctx context.Context, eventType string) error
	fetchAndClaimBatchFunc  func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error)
	markFailedFunc          func(ctx context.Context, id string, err error) error
	markPublishedFunc       func(ctx context.Context, id string) error
	recoverStuckClaimsCalls map[string]int
	markPublishedCalls      []string
	markFailedCalls         map[string]error
}

func newMockOutboxRepository() *mockOutboxRepository {
	return &mockOutboxRepository{
		recoverStuckClaimsCalls: make(map[string]int),
		markFailedCalls:         make(map[string]error),
	}
}

func (m *mockOutboxRepository) RecoverStuckClaims(ctx context.Context, eventType string) error {
	m.mu.Lock()
	m.recoverStuckClaimsCalls[eventType]++
	m.mu.Unlock()

	if m.recoverStuckClaimsFunc != nil {
		return m.recoverStuckClaimsFunc(ctx, eventType)
	}
	return nil
}

func (m *mockOutboxRepository) FetchAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.fetchAndClaimBatchFunc != nil {
		return m.fetchAndClaimBatchFunc(ctx, eventType, limit)
	}
	return nil, nil
}

func (m *mockOutboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	m.mu.Lock()
	m.markFailedCalls[id] = err
	m.mu.Unlock()

	if m.markFailedFunc != nil {
		return m.markFailedFunc(ctx, id, err)
	}
	return nil
}

func (m *mockOutboxRepository) MarkPublished(ctx context.Context, id string) error {
	m.mu.Lock()
	m.markPublishedCalls = append(m.markPublishedCalls, id)
	m.mu.Unlock()

	if m.markPublishedFunc != nil {
		return m.markPublishedFunc(ctx, id)
	}
	return nil
}

type mockTenantPublisher struct {
	mu                            sync.Mutex
	publishWorkspaceInitiatedFunc func(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error
	publishWorkspaceReadyFunc     func(ctx context.Context, evt domain.WorkspaceReadyEvent) error
	publishedInitiatedEvents      []domain.WorkspaceInitiatedEvent
	publishedReadyEvents          []domain.WorkspaceReadyEvent
}

func (m *mockTenantPublisher) PublishWorkspaceInitiated(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error {
	m.mu.Lock()
	m.publishedInitiatedEvents = append(m.publishedInitiatedEvents, evt)
	m.mu.Unlock()

	if m.publishWorkspaceInitiatedFunc != nil {
		return m.publishWorkspaceInitiatedFunc(ctx, evt)
	}
	return nil
}

func (m *mockTenantPublisher) PublishWorkspaceReady(ctx context.Context, evt domain.WorkspaceReadyEvent) error {
	m.mu.Lock()
	m.publishedReadyEvents = append(m.publishedReadyEvents, evt)
	m.mu.Unlock()

	if m.publishWorkspaceReadyFunc != nil {
		return m.publishWorkspaceReadyFunc(ctx, evt)
	}
	return nil
}

func TestNewOutboxWorker(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}

	w := NewOutboxWorker(repo, pub)

	if w == nil {
		t.Fatal("expected NewOutboxWorker to return non-nil instance")
	}
	if w.outboxRepository != repo {
		t.Errorf("expected outboxRepository to be set")
	}
	if w.publisher != pub {
		t.Errorf("expected publisher to be set")
	}
	if cap(w.wakeUpChan) != 1 {
		t.Errorf("expected wakeUpChan capacity to be 1, got %d", cap(w.wakeUpChan))
	}
	if w.debounceDelay != defaultDebounceDelay {
		t.Errorf("expected debounceDelay %v, got %v", defaultDebounceDelay, w.debounceDelay)
	}
	if w.pollInterval != defaultPollInterval {
		t.Errorf("expected pollInterval %v, got %v", defaultPollInterval, w.pollInterval)
	}
	if w.batchSize != defaultBatchSize {
		t.Errorf("expected batchSize %d, got %d", defaultBatchSize, w.batchSize)
	}
}

func TestOutboxWorker_Poke(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)

	// First Poke should write to wakeUpChan
	w.Poke()
	select {
	case <-w.wakeUpChan:
		// Success
	default:
		t.Error("expected channel to receive signal on Poke")
	}

	// Second Poke when buffer full should not block
	w.Poke()
	w.Poke() // Non-blocking drop
	select {
	case <-w.wakeUpChan:
		// Received one signal from buffer
	default:
		t.Error("expected channel to have buffer filled")
	}
}

func TestOutboxWorker_ProcessBatch_WorkspaceInitiated_Success(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)

	evt := domain.WorkspaceInitiatedEvent{
		EventID:    "evt-1",
		TenantID:   "tenant-1",
		Plan:       "ENTERPRISE",
		OwnerEmail: "admin@example.com",
		OwnerName:  "Admin",
	}
	payload, _ := json.Marshal(evt)

	msg := domain.OutboxMessage{
		ID:        "msg-1",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   payload,
	}

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.publishedInitiatedEvents) != 1 {
		t.Fatalf("expected 1 published WorkspaceInitiatedEvent, got %d", len(pub.publishedInitiatedEvents))
	}
	if pub.publishedInitiatedEvents[0].EventID != "evt-1" {
		t.Errorf("expected EventID 'evt-1', got '%s'", pub.publishedInitiatedEvents[0].EventID)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.markPublishedCalls) != 1 || repo.markPublishedCalls[0] != "msg-1" {
		t.Errorf("expected MarkPublished call for 'msg-1', got %v", repo.markPublishedCalls)
	}
}

func TestOutboxWorker_ProcessBatch_WorkspaceReady_Success(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)

	evt := domain.WorkspaceReadyEvent{
		EventID:    "evt-2",
		TenantID:   "tenant-2",
		OwnerEmail: "owner@example.com",
	}
	payload, _ := json.Marshal(evt)

	msg := domain.OutboxMessage{
		ID:        "msg-2",
		EventType: domain.RoutingKeyWorkspaceReady,
		Payload:   payload,
	}

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyWorkspaceReady)

	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.publishedReadyEvents) != 1 {
		t.Fatalf("expected 1 published WorkspaceReadyEvent, got %d", len(pub.publishedReadyEvents))
	}
	if pub.publishedReadyEvents[0].EventID != "evt-2" {
		t.Errorf("expected EventID 'evt-2', got '%s'", pub.publishedReadyEvents[0].EventID)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.markPublishedCalls) != 1 || repo.markPublishedCalls[0] != "msg-2" {
		t.Errorf("expected MarkPublished call for 'msg-2', got %v", repo.markPublishedCalls)
	}
}

func TestOutboxWorker_ProcessBatch_InvalidJSON(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)

	msg := domain.OutboxMessage{
		ID:        "msg-invalid",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   []byte("invalid-json-{"),
	}

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if err, ok := repo.markFailedCalls["msg-invalid"]; !ok || err == nil {
		t.Errorf("expected MarkFailed call for 'msg-invalid' with unmarshal error")
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.publishedInitiatedEvents) != 0 {
		t.Errorf("expected no events published on invalid JSON")
	}
}

func TestOutboxWorker_ProcessBatch_PublishError(t *testing.T) {
	repo := newMockOutboxRepository()
	pubErr := errors.New("amqp publish connection error")
	pub := &mockTenantPublisher{
		publishWorkspaceInitiatedFunc: func(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error {
			return pubErr
		},
	}
	w := NewOutboxWorker(repo, pub)

	evt := domain.WorkspaceInitiatedEvent{EventID: "evt-err"}
	payload, _ := json.Marshal(evt)
	msg := domain.OutboxMessage{
		ID:        "msg-pub-err",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   payload,
	}

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if err, ok := repo.markFailedCalls["msg-pub-err"]; !ok || !errors.Is(err, pubErr) {
		t.Errorf("expected MarkFailed call for 'msg-pub-err' with pubErr, got %v", err)
	}
	if len(repo.markPublishedCalls) != 0 {
		t.Errorf("expected MarkPublished NOT to be called on publish failure")
	}
}

func TestOutboxWorker_ProcessBatch_MarkPublishedError(t *testing.T) {
	repo := newMockOutboxRepository()
	markErr := errors.New("db connection failure")
	repo.markPublishedFunc = func(ctx context.Context, id string) error {
		return markErr
	}

	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)

	evt := domain.WorkspaceInitiatedEvent{EventID: "evt-mark-err"}
	payload, _ := json.Marshal(evt)
	msg := domain.OutboxMessage{
		ID:        "msg-mark-err",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   payload,
	}

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	// Should handle error gracefully without panicking
	w.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.markPublishedCalls) != 1 || repo.markPublishedCalls[0] != "msg-mark-err" {
		t.Errorf("expected MarkPublished to be attempted for 'msg-mark-err'")
	}
}

func TestOutboxWorker_ProcessBatch_UnknownEventType(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)

	msg := domain.OutboxMessage{
		ID:        "msg-unknown",
		EventType: "unknown.event.key",
		Payload:   []byte("{}"),
	}

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	w.processBatch(context.Background(), "unknown.event.key")

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.markPublishedCalls) != 0 || len(repo.markFailedCalls) != 0 {
		t.Errorf("expected no mark calls for unknown event type")
	}
}

func TestOutboxWorker_ProcessBatch_FetchError(t *testing.T) {
	repo := newMockOutboxRepository()
	fetchErr := errors.New("failed to claim outbox batch")
	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return nil, fetchErr
	}

	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)

	// Should log error and return without panic
	w.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)
}

func TestOutboxWorker_ProcessBatch_StuckClaimRecoveryError(t *testing.T) {
	repo := newMockOutboxRepository()
	repo.recoverStuckClaimsFunc = func(ctx context.Context, eventType string) error {
		return errors.New("recovery failed")
	}

	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)

	w.recoverAndProcess(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.recoverStuckClaimsCalls[domain.RoutingKeyWorkspaceInitiated] != 1 {
		t.Errorf("expected RecoverStuckClaims call for workspace.initiated")
	}
	if repo.recoverStuckClaimsCalls[domain.RoutingKeyWorkspaceReady] != 1 {
		t.Errorf("expected RecoverStuckClaims call for workspace.ready")
	}
}

func TestOutboxWorker_ProcessBatch_FullBatchPokesWorker(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)
	w.batchSize = 1 // Set batchSize to 1 for test

	evt := domain.WorkspaceInitiatedEvent{EventID: "evt-full"}
	payload, _ := json.Marshal(evt)
	msg := domain.OutboxMessage{
		ID:        "msg-full",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   payload,
	}

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	select {
	case <-w.wakeUpChan:
		// Verified Poke was called upon filling batchSize
	default:
		t.Error("expected Poke() to be called when messages count equals batchSize")
	}
}

func TestOutboxWorker_StartAndShutdown(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)
	w.pollInterval = 10 * time.Millisecond
	w.debounceDelay = 5 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Clean shutdown verified
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker loop did not shut down cleanly upon context cancellation")
	}
}

func TestOutboxWorker_DebounceAndProcess(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockTenantPublisher{}
	w := NewOutboxWorker(repo, pub)
	w.debounceDelay = 5 * time.Millisecond

	w.Poke()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	w.debounceAndProcess(ctx)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.recoverStuckClaimsCalls[domain.RoutingKeyWorkspaceInitiated] == 0 {
		t.Errorf("expected debounceAndProcess to execute recoverAndProcess")
	}
}
