package service

import (
	"context"
	"testing"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/provider"
	"payment-service/internal/repository"
)

type mockPaymentRepo struct {
	createFn        func(ctx context.Context, input repository.CreatePaymentInput) error
	findByIDFn      func(ctx context.Context, id string) (*domain.Payment, error)
	findByOrderIDFn func(ctx context.Context, tenantID, orderID string) (*domain.Payment, error)
	updateFn        func(ctx context.Context, input repository.UpdatePaymentInput) error
	findExpiredFn   func(ctx context.Context, ttlDuration time.Duration, limit int) ([]*domain.Payment, error)
}

func (m *mockPaymentRepo) Create(ctx context.Context, input repository.CreatePaymentInput) error {
	if m.createFn != nil {
		return m.createFn(ctx, input)
	}
	return nil
}

func (m *mockPaymentRepo) FindByID(ctx context.Context, id string) (*domain.Payment, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(ctx, id)
	}
	return &domain.Payment{ID: id, TenantID: "tnt_1", OrderID: "ord_1", Status: domain.PaymentStatusInstructionsReady}, nil
}

func (m *mockPaymentRepo) FindByOrderID(ctx context.Context, tenantID, orderID string) (*domain.Payment, error) {
	if m.findByOrderIDFn != nil {
		return m.findByOrderIDFn(ctx, tenantID, orderID)
	}
	return &domain.Payment{ID: "pay_1", TenantID: tenantID, OrderID: orderID, Status: domain.PaymentStatusInstructionsReady}, nil
}

func (m *mockPaymentRepo) FindByIDForUpdate(ctx context.Context, id string) (*domain.Payment, error) {
	return m.FindByID(ctx, id)
}

func (m *mockPaymentRepo) CreateAttempt(ctx context.Context, input repository.CreateAttemptInput) error {
	return nil
}

func (m *mockPaymentRepo) UpdateAttempt(ctx context.Context, input repository.UpdateAttemptInput) error {
	return nil
}

func (m *mockPaymentRepo) FindAttemptsByPaymentID(ctx context.Context, paymentID string) ([]*domain.PaymentAttempt, error) {
	return nil, nil
}

func (m *mockPaymentRepo) Update(ctx context.Context, input repository.UpdatePaymentInput) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, input)
	}
	return nil
}

func (m *mockPaymentRepo) FindExpiredPayments(ctx context.Context, ttlDuration time.Duration, limit int) ([]*domain.Payment, error) {
	if m.findExpiredFn != nil {
		return m.findExpiredFn(ctx, ttlDuration, limit)
	}
	return nil, nil
}

type mockInboxRepo struct{}

func (m *mockInboxRepo) TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
	return false, nil
}

func (m *mockInboxRepo) SaveInboxEvent(ctx context.Context, eventID string, eventType string) error {
	return nil
}

type mockOutboxRepo struct{}

func (m *mockOutboxRepo) SaveOutboxEvent(ctx context.Context, eventID, routingKey string, payload interface{}) error {
	return nil
}

type mockPSPConfigRepo struct {
	saveFn func(ctx context.Context, input repository.SaveConfigInput, masterKey []byte) error
	getFn  func(ctx context.Context, tenantID string, masterKey []byte) (*domain.TenantPSPConfig, error)
}

func (m *mockPSPConfigRepo) SaveConfig(ctx context.Context, input repository.SaveConfigInput, masterKey []byte) error {
	if m.saveFn != nil {
		return m.saveFn(ctx, input, masterKey)
	}
	return nil
}

func (m *mockPSPConfigRepo) GetConfig(ctx context.Context, tenantID string, masterKey []byte) (*domain.TenantPSPConfig, error) {
	if m.getFn != nil {
		return m.getFn(ctx, tenantID, masterKey)
	}
	return &domain.TenantPSPConfig{TenantID: tenantID, PriorityChain: []domain.ProviderType{domain.ProviderMock}}, nil
}

type mockTenantPSPResolver struct {
	invalidated bool
}

func (m *mockTenantPSPResolver) ResolveConfig(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error) {
	return &domain.TenantPSPConfig{TenantID: tenantID, PriorityChain: []domain.ProviderType{domain.ProviderMock}}, nil
}

func (m *mockTenantPSPResolver) InvalidateCache(tenantID string) {
	m.invalidated = true
}

type mockProviderRegistry struct{}

func (m *mockProviderRegistry) ExecuteFallbackChain(ctx context.Context, req domain.CreateSessionRequest) (*provider.ExecutionResult, error) {
	return &provider.ExecutionResult{Provider: domain.ProviderMock}, nil
}

func (m *mockProviderRegistry) GetProvider(providerID domain.ProviderType) (domain.PaymentProvider, bool) {
	return nil, false
}

func TestPaymentService_GetPaymentByID(t *testing.T) {
	repo := &mockPaymentRepo{}
	svc := NewPaymentService(nil, repo, &mockInboxRepo{}, &mockOutboxRepo{}, &mockPSPConfigRepo{}, &mockTenantPSPResolver{}, &mockProviderRegistry{}, []byte("key"), nil)

	p, err := svc.GetPaymentByID(context.Background(), "pay_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID != "pay_1" {
		t.Errorf("expected ID pay_1, got %s", p.ID)
	}
}

func TestPaymentService_GetPaymentByOrderID(t *testing.T) {
	repo := &mockPaymentRepo{}
	svc := NewPaymentService(nil, repo, &mockInboxRepo{}, &mockOutboxRepo{}, &mockPSPConfigRepo{}, &mockTenantPSPResolver{}, &mockProviderRegistry{}, []byte("key"), nil)

	p, err := svc.GetPaymentByOrderID(context.Background(), "tnt_1", "ord_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.OrderID != "ord_1" {
		t.Errorf("expected OrderID ord_1, got %s", p.OrderID)
	}
}

func TestPaymentService_SavePSPConfig(t *testing.T) {
	resolver := &mockTenantPSPResolver{}
	svc := NewPaymentService(nil, &mockPaymentRepo{}, &mockInboxRepo{}, &mockOutboxRepo{}, &mockPSPConfigRepo{}, resolver, &mockProviderRegistry{}, []byte("key"), nil)

	if err := svc.SavePSPConfig(context.Background(), &domain.TenantPSPConfig{}); err == nil {
		t.Error("expected error for empty tenant_id")
	}

	cfg := &domain.TenantPSPConfig{TenantID: "tnt_100", PriorityChain: []domain.ProviderType{domain.ProviderMock}}
	if err := svc.SavePSPConfig(context.Background(), cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resolver.invalidated {
		t.Error("expected resolver cache invalidation")
	}
}

func TestPaymentService_GetPSPConfig(t *testing.T) {
	svc := NewPaymentService(nil, &mockPaymentRepo{}, &mockInboxRepo{}, &mockOutboxRepo{}, &mockPSPConfigRepo{}, &mockTenantPSPResolver{}, &mockProviderRegistry{}, []byte("key"), nil)

	cfg, err := svc.GetPSPConfig(context.Background(), "tnt_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TenantID != "tnt_1" {
		t.Errorf("expected TenantID tnt_1, got %s", cfg.TenantID)
	}
}

func TestPaymentService_SweepExpiredPayments(t *testing.T) {
	repo := &mockPaymentRepo{
		findExpiredFn: func(ctx context.Context, ttl time.Duration, limit int) ([]*domain.Payment, error) {
			return []*domain.Payment{
				{ID: "pay_exp_1", TenantID: "tnt_1", OrderID: "ord_1", Status: domain.PaymentStatusPending},
			}, nil
		},
	}
	svc := NewPaymentService(nil, repo, &mockInboxRepo{}, &mockOutboxRepo{}, &mockPSPConfigRepo{}, &mockTenantPSPResolver{}, &mockProviderRegistry{}, []byte("key"), nil)

	count, err := svc.SweepExpiredPayments(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error in SweepExpiredPayments: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 expired payment swept, got %d", count)
	}
}
