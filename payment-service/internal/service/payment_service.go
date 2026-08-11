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

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type PaymentRepository interface {
	Create(ctx context.Context, input repository.CreatePaymentInput) error
	FindByID(ctx context.Context, id string) (*domain.Payment, error)
	FindByOrderID(ctx context.Context, tenantID, orderID string) (*domain.Payment, error)
	FindByIDForUpdate(ctx context.Context, id string) (*domain.Payment, error)
	CreateAttempt(ctx context.Context, input repository.CreateAttemptInput) error
	UpdateAttempt(ctx context.Context, input repository.UpdateAttemptInput) error
	FindAttemptsByPaymentID(ctx context.Context, paymentID string) ([]*domain.PaymentAttempt, error)
	Update(ctx context.Context, input repository.UpdatePaymentInput) error
	FindExpiredPayments(ctx context.Context, ttlDuration time.Duration, limit int) ([]*domain.Payment, error)
}

type InboxRepository interface {
	SaveInboxEvent(ctx context.Context, eventID string, eventType string) error
}

type OutboxRepository interface {
	SaveOutboxEvent(ctx context.Context, eventID, routingKey string, payload interface{}) error
}

type PSPConfigRepository interface {
	SaveConfig(ctx context.Context, input repository.SaveConfigInput, masterKey []byte) error
	GetConfig(ctx context.Context, tenantID string, masterKey []byte) (*domain.TenantPSPConfig, error)
}

type TenantPSPResolver interface {
	ResolveConfig(ctx context.Context, tenantID string) (*domain.TenantPSPConfig, error)
	InvalidateCache(tenantID string)
}

type ProviderRegistry interface {
	ExecuteFallbackChain(ctx context.Context, req domain.CreateSessionRequest) (*provider.ExecutionResult, error)
	GetProvider(providerID domain.ProviderType) (domain.PaymentProvider, bool)
}

type PaymentService struct {
	txManager           *txcontext.SQLTxManager
	paymentRepository   PaymentRepository
	inboxRepository     InboxRepository
	outboxRepository    OutboxRepository
	pspConfigRepository PSPConfigRepository
	postgresResolver    TenantPSPResolver
	registry            ProviderRegistry
	masterKey           []byte
	logger              *slog.Logger
}

func NewPaymentService(
	txManager *txcontext.SQLTxManager,
	paymentRepository PaymentRepository,
	inboxRepository InboxRepository,
	outboxRepository OutboxRepository,
	pspConfigRepository PSPConfigRepository,
	postgresResolver TenantPSPResolver,
	registry ProviderRegistry,
	masterKey []byte,
	logger *slog.Logger,
) *PaymentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PaymentService{
		txManager:           txManager,
		paymentRepository:   paymentRepository,
		inboxRepository:     inboxRepository,
		outboxRepository:    outboxRepository,
		pspConfigRepository: pspConfigRepository,
		postgresResolver:    postgresResolver,
		registry:            registry,
		masterKey:           masterKey,
		logger:              logger,
	}
}

type TenantPSPConfigOutput struct {
	TenantID        string
	PriorityChain   []domain.ProviderType
	ProviderConfigs map[domain.ProviderType]domain.ProviderCredentials
}

type PaymentOutput struct {
	ID                string
	TenantID          string
	OrderID           string
	Amount            float64
	Currency          string
	Status            domain.PaymentStatus
	Provider          domain.ProviderType
	ExternalID        string
	Instructions      domain.PaymentInstructions
	RawWebhookPayload map[string]any
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func toPSPConfigOutput(cfg *domain.TenantPSPConfig) *TenantPSPConfigOutput {
	if cfg == nil {
		return nil
	}
	return &TenantPSPConfigOutput{
		TenantID:        cfg.TenantID,
		PriorityChain:   cfg.PriorityChain,
		ProviderConfigs: cfg.ProviderConfigs,
	}
}

func toPaymentOutput(p *domain.Payment) *PaymentOutput {
	if p == nil {
		return nil
	}
	return &PaymentOutput{
		ID:                p.ID,
		TenantID:          p.TenantID,
		OrderID:           p.OrderID,
		Amount:            p.Amount,
		Currency:          p.Currency,
		Status:            p.Status,
		Provider:          p.Provider,
		ExternalID:        p.ExternalID,
		Instructions:      p.Instructions,
		RawWebhookPayload: p.RawWebhookPayload,
		CreatedAt:         p.CreatedAt,
		UpdatedAt:         p.UpdatedAt,
	}
}

func toUpdatePaymentInput(p *domain.Payment) repository.UpdatePaymentInput {
	if p == nil {
		return repository.UpdatePaymentInput{}
	}
	return repository.UpdatePaymentInput{
		ID:                p.ID,
		TenantID:          p.TenantID,
		OrderID:           p.OrderID,
		Amount:            p.Amount,
		Currency:          p.Currency,
		Status:            p.Status,
		Provider:          p.Provider,
		ExternalID:        p.ExternalID,
		Instructions:      p.Instructions,
		RawWebhookPayload: p.RawWebhookPayload,
		CreatedAt:         p.CreatedAt,
		UpdatedAt:         p.UpdatedAt,
	}
}

func (s *PaymentService) SavePSPConfig(ctx context.Context, config *domain.TenantPSPConfig) error {
	if config.TenantID == "" {
		return errors.New("tenant_id is required")
	}

	var err error
	if s.txManager != nil {
		err = s.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
			return s.pspConfigRepository.SaveConfig(txCtx, repository.SaveConfigInput{
				TenantID:        config.TenantID,
				PriorityChain:   config.PriorityChain,
				ProviderConfigs: config.ProviderConfigs,
			}, s.masterKey)
		})
	} else {
		err = s.pspConfigRepository.SaveConfig(ctx, repository.SaveConfigInput{
			TenantID:        config.TenantID,
			PriorityChain:   config.PriorityChain,
			ProviderConfigs: config.ProviderConfigs,
		}, s.masterKey)
	}
	if err != nil {
		return err
	}

	if s.postgresResolver != nil {
		s.postgresResolver.InvalidateCache(config.TenantID)
	}

	return nil
}

func (s *PaymentService) GetPSPConfig(ctx context.Context, tenantID string) (*TenantPSPConfigOutput, error) {
	if s.postgresResolver != nil {
		cfg, err := s.postgresResolver.ResolveConfig(ctx, tenantID)
		return toPSPConfigOutput(cfg), err
	}
	cfg, err := s.pspConfigRepository.GetConfig(ctx, tenantID, s.masterKey)
	return toPSPConfigOutput(cfg), err
}

func (s *PaymentService) InitiatePayment(ctx context.Context, tenantID, orderID string, amount float64, currency string) (*PaymentOutput, error) {
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

	err := s.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		return s.paymentRepository.Create(txCtx, repository.CreatePaymentInput{
			ID:                p.ID,
			TenantID:          p.TenantID,
			OrderID:           p.OrderID,
			Amount:            p.Amount,
			Currency:          p.Currency,
			Status:            p.Status,
			Provider:          p.Provider,
			ExternalID:        p.ExternalID,
			Instructions:      p.Instructions,
			RawWebhookPayload: p.RawWebhookPayload,
			CreatedAt:         p.CreatedAt,
			UpdatedAt:         p.UpdatedAt,
		})
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

	return toPaymentOutput(p), nil
}

func (s *PaymentService) GeneratePaymentInstructions(ctx context.Context, paymentID string) error {
	p, err := s.paymentRepository.FindByID(ctx, paymentID)
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

	return s.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
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
				attInput := repository.CreateAttemptInput{
					ID:           att.ID,
					PaymentID:    att.PaymentID,
					TenantID:     att.TenantID,
					Provider:     att.Provider,
					Status:       att.Status,
					ErrorMessage: att.ErrorMessage,
				}
				_ = s.paymentRepository.CreateAttempt(txCtx, attInput)
			}
		}

		if err != nil {
			// All providers failed
			p.Status = domain.PaymentStatusFailed
			_ = s.paymentRepository.Update(txCtx, toUpdatePaymentInput(p))

			outboxEvt := &domain.PaymentFailedEvent{
				EventID:   uuid.New().String(),
				TenantID:  p.TenantID,
				PaymentID: p.ID,
				OrderID:   p.OrderID,
				Reason:    err.Error(),
				Provider:  "",
			}
			return s.outboxRepository.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentFailed, outboxEvt)
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
		attInput := repository.CreateAttemptInput{
			ID:                att.ID,
			PaymentID:         att.PaymentID,
			TenantID:          att.TenantID,
			Provider:          att.Provider,
			ExternalSessionID: att.ExternalSessionID,
			Status:            att.Status,
		}
		_ = s.paymentRepository.CreateAttempt(txCtx, attInput)

		p.Status = domain.PaymentStatusInstructionsReady
		p.Provider = execRes.Provider
		p.ExternalID = execRes.Session.ExternalSessionID
		p.Instructions = execRes.Session.Instructions

		if err := s.paymentRepository.Update(txCtx, toUpdatePaymentInput(p)); err != nil {
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

		return s.outboxRepository.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentInstructionsGenerated, outboxEvt)
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

	return s.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
		// 1. Transactional Inbox Deduplication Guard
		if err := s.inboxRepository.SaveInboxEvent(txCtx, webhookEvt.EventID, string(webhookEvt.EventType)); err != nil {
			if errors.Is(err, domain.ErrDuplicateEvent) {
				s.logger.Info("ignoring duplicate webhook event", "event_id", webhookEvt.EventID)
				return nil
			}
			return err
		}

		// 2. Resolve Payment entity (prefer payment_id, fallback to external_session_id or order_id)
		var p *domain.Payment
		if webhookEvt.PaymentID != "" {
			p, err = s.paymentRepository.FindByIDForUpdate(txCtx, webhookEvt.PaymentID)
		} else if webhookEvt.OrderID != "" && webhookEvt.TenantID != "" {
			p, err = s.paymentRepository.FindByOrderID(txCtx, webhookEvt.TenantID, webhookEvt.OrderID)
			if err == nil && p != nil {
				// Relock row specifically
				p, err = s.paymentRepository.FindByIDForUpdate(txCtx, p.ID)
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
				_ = s.paymentRepository.Update(txCtx, toUpdatePaymentInput(p))
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

		if err := s.paymentRepository.Update(txCtx, toUpdatePaymentInput(p)); err != nil {
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
			if err := s.outboxRepository.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentSucceeded, outboxEvt); err != nil {
				return err
			}

			// 6. Phantom Session Double-Billing Cancellation
			attempts, _ := s.paymentRepository.FindAttemptsByPaymentID(txCtx, p.ID)
			for _, att := range attempts {
				if att.Provider != p.Provider && att.ExternalSessionID != "" && att.Status != domain.AttemptStatusCancelled {
					if otherAdapter, ok := s.registry.GetProvider(att.Provider); ok {
						_ = otherAdapter.CancelPaymentSession(ctx, att.ExternalSessionID)
						att.Status = domain.AttemptStatusCancelled
						attUpdate := repository.UpdateAttemptInput{
							ID:                att.ID,
							PaymentID:         att.PaymentID,
							TenantID:          att.TenantID,
							Provider:          att.Provider,
							ExternalSessionID: att.ExternalSessionID,
							Status:            att.Status,
							ErrorMessage:      att.ErrorMessage,
						}
						_ = s.paymentRepository.UpdateAttempt(txCtx, attUpdate)
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
			return s.outboxRepository.SaveOutboxEvent(txCtx, lateEvt.EventID, domain.RoutingKeyPaymentLateReceived, lateEvt)

		case domain.PaymentStatusFailed:
			failedEvt := &domain.PaymentFailedEvent{
				EventID:   uuid.New().String(),
				TenantID:  p.TenantID,
				PaymentID: p.ID,
				OrderID:   p.OrderID,
				Reason:    "Webhook reported payment failure",
				Provider:  p.Provider,
			}
			return s.outboxRepository.SaveOutboxEvent(txCtx, failedEvt.EventID, domain.RoutingKeyPaymentFailed, failedEvt)
		}

		return nil
	})
}

func (s *PaymentService) GetPaymentByID(ctx context.Context, id string) (*PaymentOutput, error) {
	p, err := s.paymentRepository.FindByID(ctx, id)
	return toPaymentOutput(p), err
}

func (s *PaymentService) GetPaymentByOrderID(ctx context.Context, tenantID, orderID string) (*PaymentOutput, error) {
	p, err := s.paymentRepository.FindByOrderID(ctx, tenantID, orderID)
	return toPaymentOutput(p), err
}

func (s *PaymentService) SweepExpiredPayments(ctx context.Context, ttlDuration time.Duration) (int, error) {
	expiredPayments, err := s.paymentRepository.FindExpiredPayments(ctx, ttlDuration, 100)
	if err != nil || len(expiredPayments) == 0 {
		return 0, err
	}

	count := 0
	for _, p := range expiredPayments {
		processFn := func(txCtx context.Context) error {
			lockedPayment, err := s.paymentRepository.FindByIDForUpdate(txCtx, p.ID)
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
			if err := s.paymentRepository.Update(txCtx, toUpdatePaymentInput(lockedPayment)); err != nil {
				return err
			}

			expiredEvt := &domain.PaymentExpiredEvent{
				EventID:   uuid.New().String(),
				TenantID:  lockedPayment.TenantID,
				PaymentID: lockedPayment.ID,
				OrderID:   lockedPayment.OrderID,
				ExpiredAt: time.Now(),
			}

			return s.outboxRepository.SaveOutboxEvent(txCtx, expiredEvt.EventID, domain.RoutingKeyPaymentExpired, expiredEvt)
		}

		var err error
		if s.txManager != nil {
			err = s.txManager.WithTransaction(ctx, processFn)
		} else {
			err = processFn(ctx)
		}

		if err == nil {
			count++
		}
	}

	return count, nil
}
