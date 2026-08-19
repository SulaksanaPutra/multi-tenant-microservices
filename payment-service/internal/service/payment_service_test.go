package service

import (
	"context"
	"testing"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
)

type mockPaymentRepository struct {
	createFn        func(ctx context.Context, input repository.CreatePaymentInput) error
	findByIDFn      func(ctx context.Context, id string) (*domain.Payment, error)
	findByOrderIDFn func(ctx context.Context, tenantID, orderID string) (*domain.Payment, error)
	updateFn        func(ctx context.Context, input repository.UpdatePaymentInput) error
	findExpiredFn   func(ctx context.Context, ttlDuration time.Duration, limit int) ([]*domain.Payment, error)
}

func (m *mockPaymentRepository) Create(ctx context.Context, input repository.CreatePaymentInput) error {
	if m.createFn != nil {
		return m.createFn(ctx, input)
	}
	return nil
}

func (m *mockPaymentRepository) FindByID(ctx context.Context, id string) (*domain.Payment, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(ctx, id)
	}
	return &domain.Payment{ID: id, TenantID: "tnt_1", OrderID: "ord_1", Status: domain.PaymentStatusInstructionsReady}, nil
}

func (m *mockPaymentRepository) FindByOrderID(ctx context.Context, tenantID, orderID string) (*domain.Payment, error) {
	if m.findByOrderIDFn != nil {
		return m.findByOrderIDFn(ctx, tenantID, orderID)
	}
	return &domain.Payment{ID: "pay_1", TenantID: tenantID, OrderID: orderID, Status: domain.PaymentStatusInstructionsReady}, nil
}

func (m *mockPaymentRepository) FindByIDForUpdate(ctx context.Context, id string) (*domain.Payment, error) {
	return m.FindByID(ctx, id)
}

func (m *mockPaymentRepository) CreateAttempt(ctx context.Context, input repository.CreateAttemptInput) error {
	return nil
}

func (m *mockPaymentRepository) UpdateAttempt(ctx context.Context, input repository.UpdateAttemptInput) error {
	return nil
}

func (m *mockPaymentRepository) FindAttemptsByPaymentID(ctx context.Context, paymentID string) ([]*domain.PaymentAttempt, error) {
	return nil, nil
}

func (m *mockPaymentRepository) Update(ctx context.Context, input repository.UpdatePaymentInput) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, input)
	}
	return nil
}

func (m *mockPaymentRepository) FindExpiredPayments(ctx context.Context, ttlDuration time.Duration, limit int) ([]*domain.Payment, error) {
	if m.findExpiredFn != nil {
		return m.findExpiredFn(ctx, ttlDuration, limit)
	}
	return nil, nil
}

type mockOutboxRepository struct{}

func (m *mockOutboxRepository) SaveOutboxEvent(ctx context.Context, eventID, routingKey string, payload interface{}) error {
	return nil
}

func TestPaymentService_GetPaymentByID(t *testing.T) {
	paymentRepository := &mockPaymentRepository{}
	paymentService := NewPaymentService(nil, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	p, err := paymentService.GetPaymentByID(context.Background(), "pay_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID != "pay_1" {
		t.Errorf("expected ID pay_1, got %s", p.ID)
	}
}

func TestPaymentService_GetPaymentByOrderID(t *testing.T) {
	paymentRepository := &mockPaymentRepository{}
	paymentService := NewPaymentService(nil, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	p, err := paymentService.GetPaymentByOrderID(context.Background(), "tnt_1", "ord_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.OrderID != "ord_1" {
		t.Errorf("expected OrderID ord_1, got %s", p.OrderID)
	}
}

func TestPaymentService_SweepExpiredPayments(t *testing.T) {
	paymentRepository := &mockPaymentRepository{
		findExpiredFn: func(ctx context.Context, ttl time.Duration, limit int) ([]*domain.Payment, error) {
			return []*domain.Payment{
				{ID: "pay_exp_1", TenantID: "tnt_1", OrderID: "ord_1", Status: domain.PaymentStatusPending},
			}, nil
		},
	}
	paymentService := NewPaymentService(nil, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	count, err := paymentService.SweepExpiredPayments(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error in SweepExpiredPayments: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 expired payment swept, got %d", count)
	}
}

func TestPaymentService_InitiatePayment(t *testing.T) {
	var createdInput repository.CreatePaymentInput
	paymentRepository := &mockPaymentRepository{
		createFn: func(ctx context.Context, input repository.CreatePaymentInput) error {
			createdInput = input
			return nil
		},
	}
	paymentService := NewPaymentService(nil, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	p, err := paymentService.InitiatePayment(context.Background(), InitiatePaymentInput{
		TenantID: "tnt_99",
		OrderID:  "ord_99",
		Amount:   150.0,
		Currency: "USD",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.TenantID != "tnt_99" || p.OrderID != "ord_99" || p.Amount != 150.0 {
		t.Errorf("unexpected payment output: %+v", p)
	}
	if createdInput.Status != domain.PaymentStatusPending {
		t.Errorf("expected PENDING status on create, got %s", createdInput.Status)
	}
}

func TestPaymentService_CompleteInstructionGeneration(t *testing.T) {
	var updatedInput repository.UpdatePaymentInput
	paymentRepository := &mockPaymentRepository{
		findByIDFn: func(ctx context.Context, id string) (*domain.Payment, error) {
			return &domain.Payment{
				ID:       id,
				TenantID: "tnt_1",
				OrderID:  "ord_1",
				Status:   domain.PaymentStatusPending,
			}, nil
		},
		updateFn: func(ctx context.Context, input repository.UpdatePaymentInput) error {
			updatedInput = input
			return nil
		},
	}
	paymentService := NewPaymentService(nil, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	err := paymentService.CompleteInstructionGeneration(context.Background(), CompleteInstructionInput{
		PaymentID:         "pay_1",
		Provider:          domain.ProviderDirectBank,
		ExternalSessionID: "va_123",
		Instructions: domain.PaymentInstructions{
			Type:     domain.InstructionVirtualAccount,
			VANumber: "88012999",
			BankCode: "BCA",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updatedInput.Status != domain.PaymentStatusInstructionsReady {
		t.Errorf("expected status PAYMENT_INSTRUCTIONS_READY, got %s", updatedInput.Status)
	}
}

func TestPaymentService_ProcessVerifiedWebhook(t *testing.T) {
	paymentRepository := &mockPaymentRepository{
		findByIDFn: func(ctx context.Context, id string) (*domain.Payment, error) {
			return &domain.Payment{
				ID:       id,
				TenantID: "tnt_1",
				OrderID:  "ord_1",
				Amount:   100.0,
				Currency: "USD",
				Status:   domain.PaymentStatusInstructionsReady,
			}, nil
		},
	}
	paymentService := NewPaymentService(nil, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	output, err := paymentService.ProcessVerifiedWebhook(context.Background(), ProcessVerifiedWebhookInput{
		EventID:           "evt_1",
		EventType:         domain.WebhookEventTypePaymentSucceeded,
		Provider:          domain.ProviderDirectBank,
		TenantID:          "tnt_1",
		OrderID:           "ord_1",
		PaymentID:         "pay_1",
		ExternalSessionID: "va_123",
		Amount:            100.0,
		Currency:          "USD",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Status != domain.PaymentStatusSucceeded {
		t.Errorf("expected SUCCEEDED status, got %s", output.Status)
	}
}
