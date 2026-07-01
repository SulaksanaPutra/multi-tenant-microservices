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
	repo := newMockOutboxRepository()
	pub := &mockOrderEventPublisher{}

	w := NewOutboxWorker(
		resolver,
		lister,
		status,
		func(cfg tenantdb.Config) OutboxRepository { return repo },
		pub,
	)

	if w == nil {
		t.Fatal("expected NewOutboxWorker to return non-nil instance")
	}
	if w.resolver != resolver {
		t.Error("expected resolver to be set")
	}
	if w.tenantLister != lister {
		t.Error("expected tenantLister to be set")
	}
	if w.routingStatus != status {
		t.Error("expected routingStatus to be set")
	}
	if w.orderEventPublisher != pub {
		t.Error("expected publisher to be set")
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
	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{}, nil, &mockOrderEventPublisher{})

	w.Poke()
	select {
	case <-w.wakeUpChan:
	default:
		t.Error("expected channel to receive signal on Poke")
	}

	w.Poke()
	w.Poke() // Non-blocking drop
	select {
	case <-w.wakeUpChan:
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
	w := newTestWorker(resolver, lister, status, func(cfg tenantdb.Config) OutboxRepository { return newMockOutboxRepository() }, &mockOrderEventPublisher{})

	w.forEachActiveTenant(context.Background(), func(cfg tenantdb.Config) {
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
	w := newTestWorker(resolver, lister, &mockRoutingStatusChecker{}, func(cfg tenantdb.Config) OutboxRepository { return newMockOutboxRepository() }, &mockOrderEventPublisher{})

	w.forEachActiveTenant(context.Background(), func(cfg tenantdb.Config) {
		visited = append(visited, cfg.TenantID)
	})

	if len(visited) != 1 || visited[0] != "tenant-ok" {
		t.Errorf("expected only 'tenant-ok' to be polled, got %v", visited)
	}
}

func TestOutboxWorker_ForEachActiveTenant_NilDepsDisablesPolling(t *testing.T) {
	w := newTestWorker(nil, &mockTenantLister{}, nil, nil, &mockOrderEventPublisher{})

	w.forEachActiveTenant(context.Background(), func(cfg tenantdb.Config) {
		t.Error("expected fn to never be invoked with nil dependencies")
	})
}

func TestOutboxWorker_ProcessBatch_OrderCreated_Success(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockOrderEventPublisher{}
	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return repo }, pub)

	evt := domain.OrderCreatedEvent{
		EventID:    "evt-1",
		TenantID:   "tenant-1",
		OrderID:    "order-1",
		CustomerID: "customer-1",
		Amount:     99.5,
		Status:     "PENDING",
	}
	payload, _ := json.Marshal(evt)

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-1", EventType: domain.RoutingKeyOrderCreated, Payload: payload}}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	pub.mu.Lock()
	if len(pub.publishedCreatedEvents) != 1 {
		t.Fatalf("expected 1 published OrderCreatedEvent, got %d", len(pub.publishedCreatedEvents))
	}
	if pub.publishedCreatedEvents[0].EventID != "evt-1" {
		t.Errorf("expected EventID 'evt-1', got '%s'", pub.publishedCreatedEvents[0].EventID)
	}
	pub.mu.Unlock()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.markPublishedCalls) != 1 || repo.markPublishedCalls[0] != "msg-1" {
		t.Errorf("expected MarkPublished call for 'msg-1', got %v", repo.markPublishedCalls)
	}
}

func TestOutboxWorker_ProcessBatch_SkipsMigratingTenant(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockOrderEventPublisher{}
	status := &mockRoutingStatusChecker{
		getStatusFunc: func(tenantID string) string {
			if tenantID == "tenant-migrating" {
				return "MIGRATING"
			}
			return ""
		},
	}
	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, status,
		func(cfg tenantdb.Config) OutboxRepository { return repo }, pub)

	evt := domain.OrderCreatedEvent{EventID: "evt-mig", TenantID: "tenant-migrating"}
	payload, _ := json.Marshal(evt)
	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-mig", EventType: domain.RoutingKeyOrderCreated, Payload: payload}}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	pub.mu.Lock()
	if len(pub.publishedCreatedEvents) != 0 {
		t.Error("expected no events published for a MIGRATING tenant")
	}
	pub.mu.Unlock()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if err, ok := repo.markFailedCalls["msg-mig"]; !ok || err == nil {
		t.Error("expected MarkFailed call for 'msg-mig' to reset claim during migration")
	}
}

func TestOutboxWorker_ProcessBatch_InvalidJSON(t *testing.T) {
	repo := newMockOutboxRepository()
	pub := &mockOrderEventPublisher{}
	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return repo }, pub)

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-invalid", EventType: domain.RoutingKeyOrderCreated, Payload: []byte("invalid-json-{")}}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if err, ok := repo.markFailedCalls["msg-invalid"]; !ok || err == nil {
		t.Error("expected MarkFailed call for 'msg-invalid' with unmarshal error")
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()
	if len(pub.publishedCreatedEvents) != 0 {
		t.Error("expected no events published on invalid JSON")
	}
}

func TestOutboxWorker_ProcessBatch_PublishError(t *testing.T) {
	repo := newMockOutboxRepository()
	pubErr := errors.New("amqp publish connection error")
	pub := &mockOrderEventPublisher{
		publishOrderCreatedFunc: func(ctx context.Context, evt domain.OrderCreatedEvent) error {
			return pubErr
		},
	}
	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return repo }, pub)

	evt := domain.OrderCreatedEvent{EventID: "evt-err"}
	payload, _ := json.Marshal(evt)
	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-pub-err", EventType: domain.RoutingKeyOrderCreated, Payload: payload}}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if err, ok := repo.markFailedCalls["msg-pub-err"]; !ok || !errors.Is(err, pubErr) {
		t.Errorf("expected MarkFailed call for 'msg-pub-err' with pubErr, got %v", err)
	}
	if len(repo.markPublishedCalls) != 0 {
		t.Error("expected MarkPublished NOT to be called on publish failure")
	}
}

func TestOutboxWorker_ProcessBatch_FetchError(t *testing.T) {
	repo := newMockOutboxRepository()
	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return nil, errors.New("failed to claim outbox batch")
	}

	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return repo }, &mockOrderEventPublisher{})

	// Should log error and return without panic
	w.processBatch(context.Background(), domain.RoutingKeyOrderCreated)
}

func TestOutboxWorker_ProcessBatch_UnknownEventType(t *testing.T) {
	repo := newMockOutboxRepository()
	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return repo }, &mockOrderEventPublisher{})

	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-unknown", EventType: "unknown.event.key", Payload: []byte("{}")}}, nil
	}

	w.processBatch(context.Background(), "unknown.event.key")

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.markPublishedCalls) != 0 || len(repo.markFailedCalls) != 0 {
		t.Error("expected no mark calls for unknown event type")
	}
}

func TestOutboxWorker_ProcessBatch_FullBatchPokesWorker(t *testing.T) {
	repo := newMockOutboxRepository()
	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return repo }, &mockOrderEventPublisher{})
	w.batchSize = 1

	evt := domain.OrderCreatedEvent{EventID: "evt-full"}
	payload, _ := json.Marshal(evt)
	repo.fetchAndClaimBatchFunc = func(ctx context.Context, eventType string, limit int) ([]domain.OutboxMessage, error) {
		return []domain.OutboxMessage{{ID: "msg-full", EventType: domain.RoutingKeyOrderCreated, Payload: payload}}, nil
	}

	w.processBatch(context.Background(), domain.RoutingKeyOrderCreated)

	select {
	case <-w.wakeUpChan:
	default:
		t.Error("expected Poke() to be called when messages count equals batchSize")
	}
}

func TestOutboxWorker_RecoverAndProcess_IteratesTenants(t *testing.T) {
	repo := newMockOutboxRepository()
	lister := &mockTenantLister{
		tenantIDsFunc: func() []string { return []string{"tenant-1", "tenant-2"} },
	}
	w := newTestWorker(&mockTenantDBResolver{}, lister, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return repo }, &mockOrderEventPublisher{})

	w.recoverAndProcess(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.recoverStuckClaimsCalls[domain.RoutingKeyOrderCreated] != 2 {
		t.Errorf("expected RecoverStuckClaims called once per tenant (2), got %d",
			repo.recoverStuckClaimsCalls[domain.RoutingKeyOrderCreated])
	}
}

func TestOutboxWorker_StartAndShutdown(t *testing.T) {
	w := newTestWorker(&mockTenantDBResolver{}, &mockTenantLister{}, &mockRoutingStatusChecker{},
		func(cfg tenantdb.Config) OutboxRepository { return newMockOutboxRepository() }, &mockOrderEventPublisher{})
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
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker loop did not shut down cleanly upon context cancellation")
	}
}
