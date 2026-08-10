package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"payment-service/internal/domain"
	"payment-service/internal/provider"
	"payment-service/internal/repository"
	"payment-service/internal/txcontext"
)

type PaymentService struct {
	txMgr            *txcontext.SQLTxManager
	paymentRepo      *repository.PaymentRepository
	inboxRepo        *repository.InboxRepository
	outboxRepo       *repository.OutboxRepository
	pspConfigRepo    *repository.PSPConfigRepository
	postgresResolver *provider.PostgresTenantPSPResolver
	registry         *provider.ProviderRegistry
	masterKey        []byte
	logger           *slog.Logger
}

func NewPaymentService(
	txMgr *txcontext.SQLTxManager,
	paymentRepo *repository.PaymentRepository,
	inboxRepo *repository.InboxRepository,
	outboxRepo *repository.OutboxRepository,
	pspConfigRepo *repository.PSPConfigRepository,
	postgresResolver *provider.PostgresTenantPSPResolver,
	registry *provider.ProviderRegistry,
	masterKey []byte,
	logger *slog.Logger,
) *PaymentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PaymentService{
		txMgr:            txMgr,
		paymentRepo:      paymentRepo,
		inboxRepo:        inboxRepo,
		outboxRepo:       outboxRepo,
		pspConfigRepo:    pspConfigRepo,
		postgresResolver: postgresResolver,
		registry:         registry,
		masterKey:        masterKey,
		logger:           logger,
	}
}

func (s *PaymentService) SavePSPConfig(ctx context.Context, config *domain.TenantPSPConfig) error {
	if config.TenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}

	err := s.txMgr.WithTransaction(ctx, func(txCtx context.Context) error {
		return s.pspConfigRepo.SaveConfig(txCtx, config, s.masterKey)
	})
	if err != nil {
		return err
	}

	if s.postgresResolver != nil {
		s.postgresResolver.InvalidateCache(config.TenantID)
	}

	return nil
}

func (s *PaymentService) GetPSPConfig(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error) {
	if s.postgresResolver != nil {
		return s.postgresResolver.ResolveConfig(ctx, tenantID)
	}
	return s.pspConfigRepo.GetConfig(ctx, tenantID, s.masterKey)
}


func (s *PaymentService) InitiatePayment(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*domain.Payment, error) {
	if currency == "" {
		currency = "USD"
	}

	paymentID := domain.GeneratePaymentID()
	p := &domain.Payment{
		ID:        paymentID,
		TenantID:  tenantID,
		OrderID:   orderID,
		Amount:    amount,
		Currency:  currency,
		Status:    domain.PaymentStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := s.txMgr.WithTransaction(ctx, func(txCtx context.Context) error {
		return s.paymentRepo.Create(txCtx, p)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initiate payment: %w", err)
	}

	// Trigger async payment instruction generation
	go func() {
		asyncCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.GeneratePaymentInstructions(asyncCtx, p.ID); err != nil {
			s.logger.Error("async payment instruction generation failed", "payment_id", p.ID, "err", err)
		}
	}()

	return p, nil
}

func (s *PaymentService) GeneratePaymentInstructions(ctx context.Context, paymentID string) error {
	p, err := s.paymentRepo.FindByID(ctx, paymentID)
	if err != nil {
		return err
	}

	if p.Status != domain.PaymentStatusPending {
		return nil
	}

	req := domain.CreateSessionRequest{
		TenantID:    p.TenantID,
		PaymentID:   p.ID,
		OrderID:     p.OrderID,
		Amount:      p.Amount,
		Currency:    p.Currency,
		Description: fmt.Sprintf("Order %s", p.OrderID),
		ReturnURL:   fmt.Sprintf("http://localhost:8000/orders/%s", p.OrderID),
	}

	execRes, err := s.registry.ExecuteFallbackChain(ctx, req)

	return s.txMgr.WithTransaction(ctx, func(txCtx context.Context) error {
		// Record failed attempts if any
		if execRes != nil && len(execRes.FailedAttempts) > 0 {
			for _, failedProv := range execRes.FailedAttempts {
				errMsg := ""
				if errVal, ok := execRes.AttemptErrors[failedProv]; ok && errVal != nil {
					errMsg = errVal.Error()
				}
				att := &domain.PaymentAttempt{
					ID:           domain.GenerateAttemptID(),
					PaymentID:    p.ID,
					TenantID:     p.TenantID,
					Provider:     failedProv,
					Status:       domain.AttemptStatusTimedOut,
					ErrorMessage: errMsg,
				}
				_ = s.paymentRepo.CreateAttempt(txCtx, att)
			}
		}

		if err != nil {
			// All providers failed
			p.Status = domain.PaymentStatusFailed
			_ = s.paymentRepo.Update(txCtx, p)

			outboxEvt := &domain.PaymentFailedEvent{
				EventID:   uuid.New().String(),
				TenantID:  p.TenantID,
				PaymentID: p.ID,
				OrderID:   p.OrderID,
				Reason:    err.Error(),
				Provider:  "",
			}
			return s.outboxRepo.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentFailed, outboxEvt)
		}

		// Success attempt
		att := &domain.PaymentAttempt{
			ID:                domain.GenerateAttemptID(),
			PaymentID:         p.ID,
			TenantID:          p.TenantID,
			Provider:          execRes.Provider,
			ExternalSessionID: execRes.Session.ExternalSessionID,
			Status:            domain.AttemptStatusSuccess,
		}
		_ = s.paymentRepo.CreateAttempt(txCtx, att)

		p.Status = domain.PaymentStatusInstructionsReady
		p.Provider = execRes.Provider
		p.ExternalID = execRes.Session.ExternalSessionID
		p.Instructions = execRes.Session.Instructions

		if err := s.paymentRepo.Update(txCtx, p); err != nil {
			return err
		}

		outboxEvt := &domain.PaymentInstructionsGeneratedEvent{
			EventID:      uuid.New().String(),
			TenantID:     p.TenantID,
			PaymentID:    p.ID,
			OrderID:      p.OrderID,
			Amount:       p.Amount,
			Currency:     p.Currency,
			Instructions: p.Instructions,
			Provider:     p.Provider,
		}

		return s.outboxRepo.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentInstructionsGenerated, outboxEvt)
	})
}

func (s *PaymentService) ProcessWebhook(ctx context.Context, providerID domain.ProviderType, headers map[string]string, body []byte) error {
	pAdapter, ok := s.registry.GetProvider(providerID)
	if !ok {
		return fmt.Errorf("unregistered provider for webhook: %s", providerID)
	}

	webhookEvt, err := pAdapter.VerifyWebhookSignature(ctx, headers, body)
	if err != nil {
		return fmt.Errorf("webhook signature verification failed: %w", err)
	}

	return s.txMgr.WithTransaction(ctx, func(txCtx context.Context) error {
		// 1. Transactional Inbox Deduplication Guard
		if err := s.inboxRepo.SaveInboxEvent(txCtx, webhookEvt.EventID, string(webhookEvt.EventType)); err != nil {
			if errors.Is(err, domain.ErrDuplicateEvent) {
				s.logger.Info("ignoring duplicate webhook event", "event_id", webhookEvt.EventID)
				return nil
			}
			return err
		}

		// 2. Resolve Payment entity (prefer payment_id, fallback to external_session_id or order_id)
		var p *domain.Payment
		if webhookEvt.PaymentID != "" {
			p, err = s.paymentRepo.FindByIDForUpdate(txCtx, webhookEvt.PaymentID)
		} else if webhookEvt.OrderID != "" && webhookEvt.TenantID != "" {
			p, err = s.paymentRepo.FindByOrderID(txCtx, webhookEvt.TenantID, webhookEvt.OrderID)
			if err == nil && p != nil {
				// Relock row specifically
				p, err = s.paymentRepo.FindByIDForUpdate(txCtx, p.ID)
			}
		}

		if err != nil || p == nil {
			return fmt.Errorf("failed to locate payment row for webhook: %w", err)
		}

		p.RawWebhookPayload = webhookEvt.RawPayload

		// 3. Strict Amount & Currency Verification Guard
		if webhookEvt.EventType == domain.WebhookEventTypePaymentSucceeded {
			if webhookEvt.Amount > 0 && (webhookEvt.Amount != p.Amount || webhookEvt.Currency != p.Currency) {
				s.logger.Warn("payment webhook amount mismatch detected!",
					"expected_amount", p.Amount, "received_amount", webhookEvt.Amount,
					"expected_currency", p.Currency, "received_currency", webhookEvt.Currency,
					"payment_id", p.ID)

				p.Status = domain.PaymentStatusFailedAmountMismatch
				_ = s.paymentRepo.Update(txCtx, p)
				return domain.ErrPaymentAmountMismatch
			}
		}

		// 4. State Machine Transition Validation
		var targetStatus domain.PaymentStatus
		switch webhookEvt.EventType {
		case domain.WebhookEventTypePaymentSucceeded:
			if p.Status == domain.PaymentStatusExpired {
				targetStatus = domain.PaymentStatusRequiresManualReview
			} else {
				targetStatus = domain.PaymentStatusSucceeded
			}
		case domain.WebhookEventTypePaymentFailed:
			targetStatus = domain.PaymentStatusFailed
		case domain.WebhookEventTypePaymentRefunded:
			targetStatus = domain.PaymentStatusRefunded
		default:
			targetStatus = domain.PaymentStatusSucceeded
		}

		if err := domain.ValidateStateTransition(p.Status, targetStatus); err != nil {
			s.logger.Warn("rejected illegal payment state transition", "current", p.Status, "target", targetStatus, "payment_id", p.ID)
			return err
		}

		p.Status = targetStatus
		if p.Provider == "" {
			p.Provider = providerID
		}
		if p.ExternalID == "" {
			p.ExternalID = webhookEvt.ExternalSessionID
		}

		if err := s.paymentRepo.Update(txCtx, p); err != nil {
			return err
		}

		// 5. Emit Outbox Event based on state
		switch targetStatus {
		case domain.PaymentStatusSucceeded:
			outboxEvt := &domain.PaymentSucceededEvent{
				EventID:           uuid.New().String(),
				TenantID:          p.TenantID,
				PaymentID:         p.ID,
				OrderID:           p.OrderID,
				Amount:            p.Amount,
				Currency:          p.Currency,
				Provider:          p.Provider,
				ExternalSessionID: p.ExternalID,
				SucceededAt:       time.Now(),
			}
			if err := s.outboxRepo.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentSucceeded, outboxEvt); err != nil {
				return err
			}

			// 6. Phantom Session Double-Billing Cancellation
			attempts, _ := s.paymentRepo.FindAttemptsByPaymentID(txCtx, p.ID)
			for _, att := range attempts {
				if att.Provider != p.Provider && att.ExternalSessionID != "" && att.Status != domain.AttemptStatusCancelled {
					if otherAdapter, ok := s.registry.GetProvider(att.Provider); ok {
						_ = otherAdapter.CancelPaymentSession(ctx, att.ExternalSessionID)
						att.Status = domain.AttemptStatusCancelled
						_ = s.paymentRepo.UpdateAttempt(txCtx, att)
					}
				}
			}

		case domain.PaymentStatusRequiresManualReview:
			lateEvt := &domain.PaymentLateReceivedEvent{
				EventID:           uuid.New().String(),
				TenantID:          p.TenantID,
				PaymentID:         p.ID,
				OrderID:           p.OrderID,
				Amount:            p.Amount,
				Currency:          p.Currency,
				Provider:          p.Provider,
				ExternalSessionID: p.ExternalID,
				ReceivedAt:        time.Now(),
				WebhookPayload:    webhookEvt.RawPayload,
			}
			return s.outboxRepo.SaveOutboxEvent(txCtx, lateEvt.EventID, domain.RoutingKeyPaymentLateReceived, lateEvt)

		case domain.PaymentStatusFailed:
			failedEvt := &domain.PaymentFailedEvent{
				EventID:   uuid.New().String(),
				TenantID:  p.TenantID,
				PaymentID: p.ID,
				OrderID:   p.OrderID,
				Reason:    "Webhook reported payment failure",
				Provider:  p.Provider,
			}
			return s.outboxRepo.SaveOutboxEvent(txCtx, failedEvt.EventID, domain.RoutingKeyPaymentFailed, failedEvt)
		}

		return nil
	})
}

func (s *PaymentService) GetPaymentByID(ctx context.Context, id string) (*domain.Payment, error) {
	return s.paymentRepo.FindByID(ctx, id)
}

func (s *PaymentService) GetPaymentByOrderID(ctx context.Context, tenantID, orderID string) (*domain.Payment, error) {
	return s.paymentRepo.FindByOrderID(ctx, tenantID, orderID)
}

func (s *PaymentService) SweepExpiredPayments(ctx context.Context, ttlDuration time.Duration) (int, error) {
	expiredPayments, err := s.paymentRepo.FindExpiredPayments(ctx, ttlDuration, 100)
	if err != nil || len(expiredPayments) == 0 {
		return 0, err
	}

	count := 0
	for _, p := range expiredPayments {
		err := s.txMgr.WithTransaction(ctx, func(txCtx context.Context) error {
			lockedPayment, err := s.paymentRepo.FindByIDForUpdate(txCtx, p.ID)
			if err != nil {
				return err
			}

			if lockedPayment.Status != domain.PaymentStatusPending && lockedPayment.Status != domain.PaymentStatusInstructionsReady {
				return nil
			}

			if err := domain.ValidateStateTransition(lockedPayment.Status, domain.PaymentStatusExpired); err != nil {
				return nil
			}

			lockedPayment.Status = domain.PaymentStatusExpired
			if err := s.paymentRepo.Update(txCtx, lockedPayment); err != nil {
				return err
			}

			expiredEvt := &domain.PaymentExpiredEvent{
				EventID:   uuid.New().String(),
				TenantID:  lockedPayment.TenantID,
				PaymentID: lockedPayment.ID,
				OrderID:   lockedPayment.OrderID,
				ExpiredAt: time.Now(),
			}

			return s.outboxRepo.SaveOutboxEvent(txCtx, expiredEvt.EventID, domain.RoutingKeyPaymentExpired, expiredEvt)
		})

		if err == nil {
			count++
		}
	}

	return count, nil
}
