package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"order-service/internal/domain"
	"order-service/internal/infrastructure/tenantdb"
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

type mockTenantDBResolver struct {
	getTenantDBFunc func(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

func (m *mockTenantDBResolver) GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error) {
	if m.getTenantDBFunc != nil {
		return m.getTenantDBFunc(ctx, tenantID)
	}
	return tenantdb.Config{TenantID: tenantID, SchemaName: "public"}, nil
}

type mockTenantLister struct {
	tenantIDsFunc func() []string
}

func (m *mockTenantLister) TenantIDs() []string {
	if m.tenantIDsFunc != nil {
		return m.tenantIDsFunc()
	}
	return []string{"tenant-1"}
}

type mockRoutingStatusChecker struct {
	getStatusFunc func(tenantID string) string
}

func (m *mockRoutingStatusChecker) GetStatus(tenantID string) string {
	if m.getStatusFunc != nil {
		return m.getStatusFunc(tenantID)
	}
	return ""
}

type mockOrderEventPublisher struct {
	mu                      sync.Mutex
	publishOrderCreatedFunc func(ctx context.Context, evt domain.OrderCreatedEvent) error
	publishedCreatedEvents  []domain.OrderCreatedEvent
}

func (m *mockOrderEventPublisher) PublishOrderCreated(ctx context.Context, evt domain.OrderCreatedEvent) error {
	m.mu.Lock()
	m.publishedCreatedEvents = append(m.publishedCreatedEvents, evt)
	m.mu.Unlock()

	if m.publishOrderCreatedFunc != nil {
		return m.publishOrderCreatedFunc(ctx, evt)
	}
	return nil
}

func newTestWorker(
	resolver TenantDBResolver,
	lister TenantLister,
	status RoutingStatusChecker,
	factory OutboxRepoFactory,
	publisher OrderEventPublisher,
) *OutboxWorker {
	return NewOutboxWorker(resolver, lister, status, factory, publisher)
}

func TestNewOutboxWorker(t *testing.T) {
	resolver := &mockTenantDBResolver{}
	lister := &mockTenantLister{}
	status := &mockRoutingStatusChecker{}
	outboxRepository := newMockOutboxRepository()
	orderEventPublisher := &mockOrderEventPublisher{}

	outboxWorker := NewOutboxWorker(
		resolver,
		lister,
		status,
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository },
		orderEventPublisher,
	)

	if outboxWorker == nil {
		t.Fatal("expected NewOutboxWorker to return non-nil instance")
	}
	if outboxWorker.resolver != resolver {
		t.Error("expected resolver to be set")
	}
	if outboxWorker.tenantLister != lister {
		t.Error("expected tenantLister to be set")
	}
	if outboxWorker.routingStatus != status {
		t.Error("expected routingStatus to be set")
	}
	if outboxWorker.orderEventPublisher != orderEventPublisher {
		t.Error("expected publisher to be set")
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
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{}, nil, &mockOrderEventPublisher{})

	outboxWorker.Poke()
	select {
	case <-outboxWorker.wakeUpChan:
	default:
		t.Error("expected channel to receive signal on Poke")
	}

	outboxWorker.Poke()
	outboxWorker.Poke()
	select {
	case <-outboxWorker.wakeUpChan:
	default:
		t.Error("expected channel to have buffer filled")
	}
}

func TestOutboxWorker_ForEachActiveTenant_SkipsMigrating(t *testing.T) {
	visited := make([]string, 0)
	lister := &mockTenantLister{
		tenantIDsFunc: func() []string { return []string{"tenant-locked", "tenant-active"} },
	}
	status := &mockRoutingStatusChecker{
		getStatusFunc: func(tenantID string) string {
			if tenantID == "tenant-locked" {
				return "MIGRATING"
			}
			return ""
		},
	}
	resolver := &mockTenantDBResolver{}
	outboxWorker := newTestWorker(resolver, lister, status, func(cfg tenantdb.Config) OutboxRepository { return newMockOutboxRepository() }, &mockOrderEventPublisher{})

	outboxWorker.forEachActiveTenant(context.Background(), func(cfg tenantdb.Config) {
		visited = append(visited, cfg.TenantID)
	})

	if len(visited) != 1 || visited[0] != "tenant-active" {
		t.Errorf("expected only 'tenant-active' to be polled, got %v", visited)
	}
}

func TestOutboxWorker_ForEachActiveTenant_SkipsResolverError(t *testing.T) {
	visited := make([]string, 0)
	lister := &mockTenantLister{
		tenantIDsFunc: func() []string { return []string{"tenant-fail", "tenant-ok"} },
	}
	resolver := &mockTenantDBResolver{
		getTenantDBFunc: func(ctx context.Context, tenantID string) (tenantdb.Config, error) {
			if tenantID == "tenant-fail" {
				return tenantdb.Config{}, errors.New("resolve failed")
			}
			return tenantdb.Config{TenantID: tenantID}, nil
		},
	}
	outboxWorker := newTestWorker(resolver, lister, &mockRoutingStatusChecker{}, func(cfg tenantdb.Config) OutboxRepository { return newMockOutboxRepository() }, &mockOrderEventPublisher{})

	outboxWorker.forEachActiveTenant(context.Background(), func(cfg tenantdb.Config) {
		visited = append(visited, cfg.TenantID)
	})

	if len(visited) != 1 || visited[0] != "tenant-ok" {
		t.Errorf("expected only 'tenant-ok' to be polled, got %v", visited)
	}
}

func TestOutboxWorker_ForEachActiveTenant_NilDepsDisablesPolling(t *testing.T) {
	outboxWorker := newTestWorker(nil, &mockTenantLister{}, nil, nil, &mockOrderEventPublisher{})

	outboxWorker.forEachActiveTenant(context.Background(), func(cfg tenantdb.Config) {
		t.Error("expected fn to never be invoked with nil dependencies")
	})
}

func TestOutboxWorker_ProcessBatch_OrderCreated_Success(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	orderEventPublisher := &mockOrderEventPublisher{}
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository }, orderEventPublisher)

	evt := domain.OrderCreatedEvent{
		EventID:    "evt-1",
		TenantID:   "tenant-1",
		OrderID:    "order-1",
		CustomerID: "customer-1",
		Amount:     99.5,
		Status:     "PENDING",
	}
	payload, _ := json.Marshal(evt)

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-1", EventType: domain.RoutingKeyOrderCreated, Payload: payload}}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	orderEventPublisher.mu.Lock()
	if len(orderEventPublisher.publishedCreatedEvents) != 1 {
		t.Fatalf("expected 1 published OrderCreatedEvent, got %d", len(orderEventPublisher.publishedCreatedEvents))
	}
	if orderEventPublisher.publishedCreatedEvents[0].EventID != "evt-1" {
		t.Errorf("expected EventID 'evt-1', got '%s'", orderEventPublisher.publishedCreatedEvents[0].EventID)
	}
	orderEventPublisher.mu.Unlock()

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if len(outboxRepository.markPublishedCalls) != 1 || outboxRepository.markPublishedCalls[0] != "msg-1" {
		t.Errorf("expected MarkPublished call for 'msg-1', got %v", outboxRepository.markPublishedCalls)
	}
}

func TestOutboxWorker_ProcessBatch_SkipsMigratingTenant(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	orderEventPublisher := &mockOrderEventPublisher{}
	status := &mockRoutingStatusChecker{
		getStatusFunc: func(tenantID string) string {
			if tenantID == "tenant-migrating" {
				return "MIGRATING"
			}
			return ""
		},
	}
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, status,
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository }, orderEventPublisher)

	evt := domain.OrderCreatedEvent{EventID: "evt-mig", TenantID: "tenant-migrating"}
	payload, _ := json.Marshal(evt)
	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-mig", EventType: domain.RoutingKeyOrderCreated, Payload: payload}}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	orderEventPublisher.mu.Lock()
	if len(orderEventPublisher.publishedCreatedEvents) != 0 {
		t.Error("expected no events published for a MIGRATING tenant")
	}
	orderEventPublisher.mu.Unlock()

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if err, ok := outboxRepository.markFailedCalls["msg-mig"]; !ok || err == nil {
		t.Error("expected MarkFailed call for 'msg-mig' to reset claim during migration")
	}
}

func TestOutboxWorker_ProcessBatch_InvalidJSON(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	orderEventPublisher := &mockOrderEventPublisher{}
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository }, orderEventPublisher)

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-invalid", EventType: domain.RoutingKeyOrderCreated, Payload: []byte("invalid-json-{")}}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if err, ok := outboxRepository.markFailedCalls["msg-invalid"]; !ok || err == nil {
		t.Error("expected MarkFailed call for 'msg-invalid' with unmarshal error")
	}

	orderEventPublisher.mu.Lock()
	defer orderEventPublisher.mu.Unlock()
	if len(orderEventPublisher.publishedCreatedEvents) != 0 {
		t.Error("expected no events published on invalid JSON")
	}
}

func TestOutboxWorker_ProcessBatch_PublishError(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	pubErr := errors.New("amqp publish connection error")
	orderEventPublisher := &mockOrderEventPublisher{
		publishOrderCreatedFunc: func(ctx context.Context, evt domain.OrderCreatedEvent) error {
			return pubErr
		},
	}
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository }, orderEventPublisher)

	evt := domain.OrderCreatedEvent{EventID: "evt-err"}
	payload, _ := json.Marshal(evt)
	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-pub-err", EventType: domain.RoutingKeyOrderCreated, Payload: payload}}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if err, ok := outboxRepository.markFailedCalls["msg-pub-err"]; !ok || !errors.Is(err, pubErr) {
		t.Errorf("expected MarkFailed call for 'msg-pub-err' with pubErr, got %v", err)
	}
	if len(outboxRepository.markPublishedCalls) != 0 {
		t.Error("expected MarkPublished NOT to be called on publish failure")
	}
}

func TestOutboxWorker_ProcessBatch_FetchError(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return nil, errors.New("failed to claim outbox batch")
	}

	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository }, &mockOrderEventPublisher{})

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyOrderCreated)
}

func TestOutboxWorker_ProcessBatch_UnknownEventType(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository }, &mockOrderEventPublisher{})

	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-unknown", EventType: "unknown.event.key", Payload: []byte("{}")}}, nil
	}

	outboxWorker.processBatch(context.Background(), "unknown.event.key")

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if len(outboxRepository.markPublishedCalls) != 0 || len(outboxRepository.markFailedCalls) != 0 {
		t.Error("expected no mark calls for unknown event type")
	}
}

func TestOutboxWorker_ProcessBatch_FullBatchPokesWorker(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository }, &mockOrderEventPublisher{})
	outboxWorker.batchSize = 1

	evt := domain.OrderCreatedEvent{EventID: "evt-full"}
	payload, _ := json.Marshal(evt)
	outboxRepository.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-full", EventType: domain.RoutingKeyOrderCreated, Payload: payload}}, nil
	}

	outboxWorker.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	select {
	case <-outboxWorker.wakeUpChan:
	default:
		t.Error("expected Poke() to be called when messages count equals batchSize")
	}
}

func TestOutboxWorker_RecoverAndProcess_IteratesTenants(t *testing.T) {
	outboxRepository := newMockOutboxRepository()
	lister := &mockTenantLister{
		tenantIDsFunc: func() []string { return []string{"tenant-1", "tenant-2"} },
	}
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, lister, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return outboxRepository }, &mockOrderEventPublisher{})

	outboxWorker.recoverAndProcess(context.Background())

	outboxRepository.mu.Lock()
	defer outboxRepository.mu.Unlock()
	if outboxRepository.recoverStuckClaimsCalls[domain.RoutingKeyOrderCreated] != 2 {
		t.Errorf("expected RecoverStuckClaims called once per tenant (2), got %d",
			outboxRepository.recoverStuckClaimsCalls[domain.RoutingKeyOrderCreated])
	}
}

func TestOutboxWorker_StartAndShutdown(t *testing.T) {
	outboxWorker := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return newMockOutboxRepository() }, &mockOrderEventPublisher{})
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
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker loop did not shut down cleanly upon context cancellation")
	}
}
