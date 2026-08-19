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

func (m *mockOutboxRepository) ListAndClaimBatch(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
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
	mu                               sync.Mutex
	publishWorkspaceInitiatedFunc    func(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error
	publishWorkspaceReadyFunc        func(ctx context.Context, evt domain.WorkspaceReadyEvent) error
	publishInfrastructureLockingFunc func(ctx context.Context, evt domain.InfrastructureLockingEvent) error
	publishInfraChangedFunc          func(ctx context.Context, evt domain.InfraChangedEvent) error
	publishMigrationFailedFunc       func(ctx context.Context, evt domain.TenantMigrationFailedEvent) error
	publishedInitiatedEvents         []domain.WorkspaceInitiatedEvent
	publishedReadyEvents             []domain.WorkspaceReadyEvent
	publishedInfraChangedEvents      []domain.InfraChangedEvent
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

func (m *mockTenantPublisher) PublishInfraChanged(ctx context.Context, evt domain.InfraChangedEvent) error {
	m.mu.Lock()
	m.publishedInfraChangedEvents = append(m.publishedInfraChangedEvents, evt)
	m.mu.Unlock()

	if m.publishInfraChangedFunc != nil {
		return m.publishInfraChangedFunc(ctx, evt)
	}
	return nil
}

func (m *mockTenantPublisher) PublishInfrastructureLocking(ctx context.Context, evt domain.InfrastructureLockingEvent) error {
	if m.publishInfrastructureLockingFunc != nil {
		return m.publishInfrastructureLockingFunc(ctx, evt)
	}
	return nil
}

func (m *mockTenantPublisher) PublishMigrationFailed(ctx context.Context, evt domain.TenantMigrationFailedEvent) error {
	if m.publishMigrationFailedFunc != nil {
		return m.publishMigrationFailedFunc(ctx, evt)
	}
	return nil
}

func TestNewOutboxWorker(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}

	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	if outboxWorker == nil {
		t.Fatal("expected NewOutboxWorker to return non-nil instance")
	}
	if outboxWorker.outboxRepository != outboxRepository {
		t.Errorf("expected outboxRepository to be set")
	}
	if outboxWorker.publisher != mockTenantPublisher {
		t.Errorf("expected publisher to be set")
	}
	if cap(outboxWorker.wakeUpChan) != 1 {
		t.Errorf("expected wakeUpChan capacity to be 1, got %d", cap(outboxWorker.wakeUpChan))
	}
	if outboxWorker.debounceDelay != defaultDebounceDelay {
		t.Errorf("expected debounceDelay %v, got %v", defaultDebounceDelay, outboxWorker.debounceDelay)
	}
	if outboxWorker.pollInterval != defaultPollInterval {
		t.Errorf("expected pollInterval %v, got %v", defaultPollInterval, outboxWorker.pollInterval)
	}
	if outboxWorker.batchSize != defaultBatchSize {
		t.Errorf("expected batchSize %d, got %d", defaultBatchSize, outboxWorker.batchSize)
	}
}

func TestOutboxWorker_Poke(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	// First Poke should write to wakeUpChan
	outboxWorker.Poke()
	select {
	case <-outboxWorker.wakeUpChan:
		// Success
	default:
		t.Error("expected channel to receive signal on Poke")
	}

	// Second Poke when buffer full should not block
	outboxWorker.Poke()
	outboxWorker.Poke() // Non-blocking drop
	select {
	case <-outboxWorker.wakeUpChan:
		// Received one signal from buffer
	default:
		t.Error("expected channel to have buffer filled")
	}
}

func TestOutboxWorker_ProcessBatch_WorkspaceInitiated_Success(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

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

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	mockTenantPublisher.mu.Lock()
	defer mockTenantPublisher.mu.Unlock()
	if len(mockTenantPublisher.publishedInitiatedEvents) != 1 {
		t.Fatalf("expected 1 published WorkspaceInitiatedEvent, got %d", len(mockTenantPublisher.publishedInitiatedEvents))
	}
	if mockTenantPublisher.publishedInitiatedEvents[0].EventID != "evt-1" {
		t.Errorf("expected EventID 'evt-1', got '%s'", mockTenantPublisher.publishedInitiatedEvents[0].EventID)
	}

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if len(outboxRepository.markPublishedCalls) != 1 || outboxRepository.markPublishedCalls[0] != "msg-1" {
		t.Errorf("expected MarkPublished call for 'msg-1', got %v", outboxRepository.markPublishedCalls)
	}
}

func TestOutboxWorker_ProcessBatch_WorkspaceReady_Success(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	evt := domain.WorkspaceReadyEvent{
		EventID:    "evt-2",
		TenantID:   "tenant-2",
		OwnerEmail: "owner@example.com",
		TenantName: "Acme Corp",
		TenantSlug: "acme-corp",
		OwnerName:  "Bob Jones",
	}
	payload, _ := json.Marshal(evt)

	msg := domain.OutboxMessage{
		ID:        "msg-2",
		EventType: domain.RoutingKeyWorkspaceReady,
		Payload:   payload,
	}

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyWorkspaceReady)

	mockTenantPublisher.mu.Lock()
	defer mockTenantPublisher.mu.Unlock()
	if len(mockTenantPublisher.publishedReadyEvents) != 1 {
		t.Fatalf("expected 1 published WorkspaceReadyEvent, got %d", len(mockTenantPublisher.publishedReadyEvents))
	}
	if mockTenantPublisher.publishedReadyEvents[0].EventID != "evt-2" {
		t.Errorf("expected EventID 'evt-2', got '%s'", mockTenantPublisher.publishedReadyEvents[0].EventID)
	}

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if len(outboxRepository.markPublishedCalls) != 1 || outboxRepository.markPublishedCalls[0] != "msg-2" {
		t.Errorf("expected MarkPublished call for 'msg-2', got %v", outboxRepository.markPublishedCalls)
	}
}

func TestOutboxWorker_ProcessBatch_InvalidJSON(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	msg := domain.OutboxMessage{
		ID:        "msg-invalid",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   []byte("invalid-json-{"),
	}

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if err, ok := outboxRepository.markFailedCalls["msg-invalid"]; !ok || err == nil {
		t.Errorf("expected MarkFailed call for 'msg-invalid' with unmarshal error")
	}

	mockTenantPublisher.mu.Lock()
	defer mockTenantPublisher.mu.Unlock()
	if len(mockTenantPublisher.publishedInitiatedEvents) != 0 {
		t.Errorf("expected no events published on invalid JSON")
	}
}

func TestOutboxWorker_ProcessBatch_PublishError(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	pubErr := errors.New("amqp publish connection error")
	mockTenantPublisher := &mockTenantPublisher{
		publishWorkspaceInitiatedFunc: func(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error {
			return pubErr
		},
	}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	evt := domain.WorkspaceInitiatedEvent{EventID: "evt-err"}
	payload, _ := json.Marshal(evt)
	msg := domain.OutboxMessage{
		ID:        "msg-pub-err",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   payload,
	}

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if err, ok := outboxRepository.markFailedCalls["msg-pub-err"]; !ok || !errors.Is(err, pubErr) {
		t.Errorf("expected MarkFailed call for 'msg-pub-err' with pubErr, got %v", err)
	}
	if len(outboxRepository.markPublishedCalls) != 0 {
		t.Errorf("expected MarkPublished NOT to be called on publish failure")
	}
}

func TestOutboxWorker_ProcessBatch_MarkPublishedError(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	markErr := errors.New("db connection failure")
	outboxRepository.markPublishedFunc = func(ctx context.Context, id string) error {
		return markErr
	}

	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	evt := domain.WorkspaceInitiatedEvent{EventID: "evt-mark-err"}
	payload, _ := json.Marshal(evt)
	msg := domain.OutboxMessage{
		ID:        "msg-mark-err",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   payload,
	}

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	// Should handle error gracefully without panicking
	outboxWorker.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if len(outboxRepository.markPublishedCalls) != 1 || outboxRepository.markPublishedCalls[0] != "msg-mark-err" {
		t.Errorf("expected MarkPublished to be attempted for 'msg-mark-err'")
	}
}

func TestOutboxWorker_ProcessBatch_UnknownEventType(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	msg := domain.OutboxMessage{
		ID:        "msg-unknown",
		EventType: "unknown.event.key",
		Payload:   []byte("{}"),
	}

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	outboxWorker.processBatch(context.Background(), "unknown.event.key")

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if len(outboxRepository.markPublishedCalls) != 0 || len(outboxRepository.markFailedCalls) != 0 {
		t.Errorf("expected no mark calls for unknown event type")
	}
}

func TestOutboxWorker_ProcessBatch_FetchError(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	fetchErr := errors.New("failed to claim outbox batch")
	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return nil, fetchErr
	}

	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	// Should log error and return without panic
	outboxWorker.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)
}

func TestOutboxWorker_ProcessBatch_StuckClaimRecoveryError(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	outboxRepository.recoverStuckClaimsFunc = func(ctx context.Context, eventType string) error {
		return errors.New("recovery failed")
	}

	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)

	outboxWorker.recoverAndProcess(context.Background())

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if outboxRepository.recoverStuckClaimsCalls[domain.RoutingKeyWorkspaceInitiated] != 1 {
		t.Errorf("expected RecoverStuckClaims call for workspace.initiated")
	}
	if outboxRepository.recoverStuckClaimsCalls[domain.RoutingKeyWorkspaceReady] != 1 {
		t.Errorf("expected RecoverStuckClaims call for workspace.ready")
	}
}

func TestOutboxWorker_ProcessBatch_FullBatchPokesWorker(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)
	outboxWorker.batchSize = 1 // Set batchSize to 1 for test

	evt := domain.WorkspaceInitiatedEvent{EventID: "evt-full"}
	payload, _ := json.Marshal(evt)
	msg := domain.OutboxMessage{
		ID:        "msg-full",
		EventType: domain.RoutingKeyWorkspaceInitiated,
		Payload:   payload,
	}

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{msg}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyWorkspaceInitiated)

	select {
	case <-outboxWorker.wakeUpChan:
		// Verified Poke was called upon filling batchSize
	default:
		t.Error("expected Poke() to be called when messages count equals batchSize")
	}
}

func TestOutboxWorker_StartAndShutdown(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)
	outboxWorker.pollInterval = 10 * time.Millisecond
	outboxWorker.debounceDelay = 5 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		outboxWorker.Start(ctx)
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
	outboxRepository := newMockOutboxRepository()
	mockTenantPublisher := &mockTenantPublisher{}
	outboxWorker := NewOutboxWorker(outboxRepository, mockTenantPublisher)
	outboxWorker.debounceDelay = 5 * time.Millisecond

	outboxWorker.Poke()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	outboxWorker.debounceAndProcess(ctx)

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if outboxRepository.recoverStuckClaimsCalls[domain.RoutingKeyWorkspaceInitiated] == 0 {
		t.Errorf("expected debounceAndProcess to execute recoverAndProcess")
	}
}
