package service

import (
	"context"
	"testing"
	"time"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
)

type mockDebtRepository struct {
	createFn        func(ctx context.Context, input repository.CreateDebtInput) error
	updateFn        func(ctx context.Context, input repository.UpdateDebtInput) error
	findByIDFn      func(ctx context.Context, id string) (*domain.PayableDebt, error)
	findByOrderIDFn func(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error)
}

func (m *mockDebtRepository) Create(ctx context.Context, input repository.CreateDebtInput) error {
	if m.createFn != nil {
		return m.createFn(ctx, input)
	}
	return nil
}

func (m *mockDebtRepository) Update(ctx context.Context, input repository.UpdateDebtInput) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, input)
	}
	return nil
}

func (m *mockDebtRepository) FindByID(ctx context.Context, id string) (*domain.PayableDebt, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(ctx, id)
	}
	return &domain.PayableDebt{ID: id, TenantID: "tnt_1", OrderID: "ord_1", TotalAmount: 100, PaidAmount: 0, Currency: "USD", Status: domain.DebtStatusUnpaid}, nil
}

func (m *mockDebtRepository) FindByIDForUpdate(ctx context.Context, id string) (*domain.PayableDebt, error) {
	return m.FindByID(ctx, id)
}

func (m *mockDebtRepository) FindByOrderID(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error) {
	if m.findByOrderIDFn != nil {
		return m.findByOrderIDFn(ctx, tenantID, orderID)
	}
	return &domain.PayableDebt{ID: "debt_1", TenantID: tenantID, OrderID: orderID, TotalAmount: 100, PaidAmount: 0, Currency: "USD", Status: domain.DebtStatusUnpaid}, nil
}

func (m *mockDebtRepository) FindByOrderIDForUpdate(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error) {
	return m.FindByOrderID(ctx, tenantID, orderID)
}

type mockPaymentRepository struct {
	createFn         func(ctx context.Context, input repository.CreatePaymentInput) error
	findByIDFn       func(ctx context.Context, id string) (*domain.Payment, error)
	findByOrderIDFn  func(ctx context.Context, tenantID, orderID string) (*domain.Payment, error)
	findByExternalFn func(ctx context.Context, provider domain.ProviderType, externalID string) (*domain.Payment, error)
	findByDebtIDFn   func(ctx context.Context, debtID string) ([]*domain.Payment, error)
	updateFn         func(ctx context.Context, input repository.UpdatePaymentInput) error
	findExpiredFn    func(ctx context.Context, ttlDuration time.Duration, limit int) ([]*domain.Payment, error)
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
	return &domain.Payment{ID: id, DebtID: "debt_1", TenantID: "tnt_1", OrderID: "ord_1", Status: domain.PaymentStatusInstructionsReady}, nil
}

func (m *mockPaymentRepository) FindByOrderID(ctx context.Context, tenantID, orderID string) (*domain.Payment, error) {
	if m.findByOrderIDFn != nil {
		return m.findByOrderIDFn(ctx, tenantID, orderID)
	}
	return &domain.Payment{ID: "pay_1", DebtID: "debt_1", TenantID: tenantID, OrderID: orderID, Status: domain.PaymentStatusInstructionsReady}, nil
}

func (m *mockPaymentRepository) FindByExternalID(ctx context.Context, provider domain.ProviderType, externalID string) (*domain.Payment, error) {
	if m.findByExternalFn != nil {
		return m.findByExternalFn(ctx, provider, externalID)
	}
	return &domain.Payment{ID: "pay_1", DebtID: "debt_1", TenantID: "tnt_1", OrderID: "ord_1", Status: domain.PaymentStatusInstructionsReady}, nil
}

func (m *mockPaymentRepository) FindByDebtID(ctx context.Context, debtID string) ([]*domain.Payment, error) {
	if m.findByDebtIDFn != nil {
		return m.findByDebtIDFn(ctx, debtID)
	}
	return nil, nil
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
	paymentService := NewPaymentService(nil, &mockDebtRepository{}, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

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
	paymentService := NewPaymentService(nil, &mockDebtRepository{}, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	p, err := paymentService.GetPaymentByOrderID(context.Background(), "tnt_1", "ord_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.OrderID != "ord_1" {
		t.Errorf("expected OrderID ord_1, got %s", p.OrderID)
	}
}

func TestPaymentService_InitiatePaymentSession(t *testing.T) {
	var createdPayment repository.CreatePaymentInput
	debtRepository := &mockDebtRepository{
		findByOrderIDFn: func(ctx context.Context, tenantID, orderID string) (*domain.PayableDebt, error) {
			return &domain.PayableDebt{
				ID:          "debt_123",
				TenantID:    tenantID,
				OrderID:     orderID,
				TotalAmount: 200.0,
				PaidAmount:  50.0,
				Currency:    "USD",
				Status:      domain.DebtStatusPartiallyPaid,
			}, nil
		},
	}
	paymentRepository := &mockPaymentRepository{
		findByDebtIDFn: func(ctx context.Context, debtID string) ([]*domain.Payment, error) {
			return []*domain.Payment{
				{
					ID:         "pay_old",
					DebtID:     debtID,
					Status:     domain.PaymentStatusInstructionsReady,
					Provider:   domain.ProviderDirectBank,
					ExternalID: "va_old",
				},
			}, nil
		},
		createFn: func(ctx context.Context, input repository.CreatePaymentInput) error {
			createdPayment = input
			return nil
		},
	}
	paymentService := NewPaymentService(nil, debtRepository, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	out, err := paymentService.InitiatePaymentSession(context.Background(), InitiatePaymentSessionInput{
		TenantID:      "tnt_1",
		OrderID:       "ord_1",
		PaymentMethod: "bca_va",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.CancelledSessions) != 1 {
		t.Fatalf("expected 1 cancelled previous session, got %d", len(out.CancelledSessions))
	}
	if out.CancelledSessions[0].PaymentID != "pay_old" {
		t.Errorf("expected cancelled payment pay_old, got %s", out.CancelledSessions[0].PaymentID)
	}
	if out.Payment.Amount != 150.0 {
		t.Errorf("expected remaining balance of 150.0, got %f", out.Payment.Amount)
	}
	if createdPayment.DebtID != "debt_123" {
		t.Errorf("expected DebtID debt_123, got %s", createdPayment.DebtID)
	}
}

func TestPaymentService_SweepExpiredPayments(t *testing.T) {
	paymentRepository := &mockPaymentRepository{
		findExpiredFn: func(ctx context.Context, ttl time.Duration, limit int) ([]*domain.Payment, error) {
			return []*domain.Payment{
				{ID: "pay_exp_1", DebtID: "debt_1", TenantID: "tnt_1", OrderID: "ord_1", Status: domain.PaymentStatusPending},
			}, nil
		},
	}
	paymentService := NewPaymentService(nil, &mockDebtRepository{}, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	count, err := paymentService.SweepExpiredPayments(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error in SweepExpiredPayments: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 expired payment swept, got %d", count)
	}
}

func TestPaymentService_CompleteInstructionGeneration(t *testing.T) {
	var updatedInput repository.UpdatePaymentInput
	paymentRepository := &mockPaymentRepository{
		findByIDFn: func(ctx context.Context, id string) (*domain.Payment, error) {
			return &domain.Payment{
				ID:       id,
				DebtID:   "debt_1",
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
	paymentService := NewPaymentService(nil, &mockDebtRepository{}, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

	err := paymentService.CompleteInstructionGeneration(context.Background(), CompleteInstructionInput{
		PaymentID:         "pay_1",
		Provider:          domain.ProviderDirectBank,
		PaymentMethod:     "bca_va",
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
	debtRepository := &mockDebtRepository{
		findByIDFn: func(ctx context.Context, id string) (*domain.PayableDebt, error) {
			return &domain.PayableDebt{
				ID:          id,
				TenantID:    "tnt_1",
				OrderID:     "ord_1",
				TotalAmount: 100.0,
				PaidAmount:  0.0,
				Currency:    "USD",
				Status:      domain.DebtStatusUnpaid,
			}, nil
		},
	}
	paymentRepository := &mockPaymentRepository{
		findByIDFn: func(ctx context.Context, id string) (*domain.Payment, error) {
			return &domain.Payment{
				ID:       id,
				DebtID:   "debt_1",
				TenantID: "tnt_1",
				OrderID:  "ord_1",
				Amount:   100.0,
				Currency: "USD",
				Status:   domain.PaymentStatusInstructionsReady,
			}, nil
		},
	}
	paymentService := NewPaymentService(nil, debtRepository, paymentRepository, &mockInboxRepository{}, &mockOutboxRepository{}, nil)

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
	if output.DebtStatus != domain.DebtStatusPaid {
		t.Errorf("expected PAID debt status, got %s", output.DebtStatus)
	}
}
