package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"payment-service/internal/domain"
	"payment-service/internal/repository"
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
	TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
	SaveInboxEvent(ctx context.Context, eventID string, eventType string) error
}

type OutboxRepository interface {
	SaveOutboxEvent(ctx context.Context, eventID, routingKey string, payload interface{}) error
}

type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

type PaymentService struct {
	txManager         TxManager
	paymentRepository PaymentRepository
	inboxRepository   InboxRepository
	outboxRepository  OutboxRepository
	logger            *slog.Logger
}

func NewPaymentService(
	txManager TxManager,
	paymentRepository PaymentRepository,
	inboxRepository InboxRepository,
	outboxRepository OutboxRepository,
	logger *slog.Logger,
) *PaymentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PaymentService{
		txManager:         txManager,
		paymentRepository: paymentRepository,
		inboxRepository:   inboxRepository,
		outboxRepository:  outboxRepository,
		logger:            logger,
	}
}

type InitiatePaymentInput struct {
	TenantID string
	OrderID  string
	Amount   float64
	Currency string
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

type CompleteInstructionInput struct {
	PaymentID         string
	Provider          domain.ProviderType
	ExternalSessionID string
	Instructions      domain.PaymentInstructions
	FailedAttempts    []domain.ProviderType
	AttemptErrors     map[domain.ProviderType]error
}

type FailInstructionInput struct {
	PaymentID      string
	Reason         string
	FailedAttempts []domain.ProviderType
	AttemptErrors  map[domain.ProviderType]error
}

type ProcessVerifiedWebhookInput struct {
	EventID           string
	EventType         domain.WebhookEventType
	Provider          domain.ProviderType
	TenantID          string
	OrderID           string
	PaymentID         string
	ExternalSessionID string
	Amount            float64
	Currency          string
	RawPayload        map[string]any
}

type CancelledAttemptOutput struct {
	Provider          domain.ProviderType
	ExternalSessionID string
}

type ProcessWebhookOutput struct {
	PaymentID         string
	Status            domain.PaymentStatus
	CancelledAttempts []CancelledAttemptOutput
}

func toUpdatePaymentInput(p *domain.Payment) repository.UpdatePaymentInput {
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

func (paymentService *PaymentService) InitiatePayment(ctx context.Context, input InitiatePaymentInput) (*PaymentOutput, error) {
	if input.TenantID == "" || input.OrderID == "" {
		return nil, errors.New("tenant_id and order_id are required")
	}

	currency := input.Currency
	if currency == "" {
		currency = "USD"
	}

	paymentID := domain.GeneratePaymentID()
	p := &domain.Payment{
		ID:        paymentID,
		TenantID:  input.TenantID,
		OrderID:   input.OrderID,
		Amount:    input.Amount,
		Currency:  currency,
		Status:    domain.PaymentStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	createInput := repository.CreatePaymentInput{
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

	createFn := func(txCtx context.Context) error {
		return paymentService.paymentRepository.Create(txCtx, createInput)
	}

	var err error
	if paymentService.txManager != nil {
		err = paymentService.txManager.WithTransaction(ctx, createFn)
	} else {
		err = createFn(ctx)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initiate payment: %w", err)
	}

	return toPaymentOutput(p), nil
}

func (paymentService *PaymentService) CompleteInstructionGeneration(ctx context.Context, input CompleteInstructionInput) error {
	actionFn := func(txCtx context.Context) error {
		p, err := paymentService.paymentRepository.FindByID(txCtx, input.PaymentID)
		if err != nil {
			return err
		}

		if p.Status != domain.PaymentStatusPending {
			return nil
		}

		// Record failed attempts if any
		if len(input.FailedAttempts) > 0 {
			for _, failedProv := range input.FailedAttempts {
				errMsg := ""
				if errVal, ok := input.AttemptErrors[failedProv]; ok && errVal != nil {
					errMsg = errVal.Error()
				}
				attInput := repository.CreateAttemptInput{
					ID:           domain.GenerateAttemptID(),
					PaymentID:    p.ID,
					TenantID:     p.TenantID,
					Provider:     failedProv,
					Status:       domain.AttemptStatusTimedOut,
					ErrorMessage: errMsg,
				}
				_ = paymentService.paymentRepository.CreateAttempt(txCtx, attInput)
			}
		}

		// Record successful attempt
		attInput := repository.CreateAttemptInput{
			ID:                domain.GenerateAttemptID(),
			PaymentID:         p.ID,
			TenantID:          p.TenantID,
			Provider:          input.Provider,
			ExternalSessionID: input.ExternalSessionID,
			Status:            domain.AttemptStatusSuccess,
		}
		_ = paymentService.paymentRepository.CreateAttempt(txCtx, attInput)

		p.Status = domain.PaymentStatusInstructionsReady
		p.Provider = input.Provider
		p.ExternalID = input.ExternalSessionID
		p.Instructions = input.Instructions

		if err := paymentService.paymentRepository.Update(txCtx, toUpdatePaymentInput(p)); err != nil {
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

		return paymentService.outboxRepository.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentInstructionsGenerated, outboxEvt)
	}

	if paymentService.txManager != nil {
		return paymentService.txManager.WithTransaction(ctx, actionFn)
	}
	return actionFn(ctx)
}

func (paymentService *PaymentService) FailInstructionGeneration(ctx context.Context, input FailInstructionInput) error {
	actionFn := func(txCtx context.Context) error {
		p, err := paymentService.paymentRepository.FindByID(txCtx, input.PaymentID)
		if err != nil {
			return err
		}

		if len(input.FailedAttempts) > 0 {
			for _, failedProv := range input.FailedAttempts {
				errMsg := ""
				if errVal, ok := input.AttemptErrors[failedProv]; ok && errVal != nil {
					errMsg = errVal.Error()
				}
				attInput := repository.CreateAttemptInput{
					ID:           domain.GenerateAttemptID(),
					PaymentID:    p.ID,
					TenantID:     p.TenantID,
					Provider:     failedProv,
					Status:       domain.AttemptStatusTimedOut,
					ErrorMessage: errMsg,
				}
				_ = paymentService.paymentRepository.CreateAttempt(txCtx, attInput)
			}
		}

		p.Status = domain.PaymentStatusFailed
		if err := paymentService.paymentRepository.Update(txCtx, toUpdatePaymentInput(p)); err != nil {
			return err
		}

		outboxEvt := &domain.PaymentFailedEvent{
			EventID:   uuid.New().String(),
			TenantID:  p.TenantID,
			PaymentID: p.ID,
			OrderID:   p.OrderID,
			Reason:    input.Reason,
			Provider:  "",
		}

		return paymentService.outboxRepository.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentFailed, outboxEvt)
	}

	if paymentService.txManager != nil {
		return paymentService.txManager.WithTransaction(ctx, actionFn)
	}
	return actionFn(ctx)
}

func (paymentService *PaymentService) ProcessVerifiedWebhook(ctx context.Context, input ProcessVerifiedWebhookInput) (*ProcessWebhookOutput, error) {
	var output *ProcessWebhookOutput

	actionFn := func(txCtx context.Context) error {
		// 1. Transactional Inbox Deduplication Guard
		if err := paymentService.inboxRepository.SaveInboxEvent(txCtx, input.EventID, string(input.EventType)); err != nil {
			if errors.Is(err, domain.ErrDuplicateEvent) {
				paymentService.logger.Info("ignoring duplicate webhook event", "event_id", input.EventID)
				output = &ProcessWebhookOutput{
					PaymentID: input.PaymentID,
				}
				return nil
			}
			return err
		}

		// 2. Resolve & Lock Payment entity
		var p *domain.Payment
		var err error
		if input.PaymentID != "" {
			p, err = paymentService.paymentRepository.FindByIDForUpdate(txCtx, input.PaymentID)
		} else if input.OrderID != "" && input.TenantID != "" {
			p, err = paymentService.paymentRepository.FindByOrderID(txCtx, input.TenantID, input.OrderID)
			if err == nil && p != nil {
				p, err = paymentService.paymentRepository.FindByIDForUpdate(txCtx, p.ID)
			}
		}

		if err != nil || p == nil {
			return fmt.Errorf("failed to locate payment row for webhook: %w", err)
		}

		p.RawWebhookPayload = input.RawPayload

		// 3. Strict Amount & Currency Verification Guard
		if input.EventType == domain.WebhookEventTypePaymentSucceeded {
			if input.Amount > 0 && (input.Amount != p.Amount || input.Currency != p.Currency) {
				paymentService.logger.Warn("payment webhook amount mismatch detected!",
					"expected_amount", p.Amount, "received_amount", input.Amount,
					"expected_currency", p.Currency, "received_currency", input.Currency,
					"payment_id", p.ID)

				p.Status = domain.PaymentStatusFailedAmountMismatch
				_ = paymentService.paymentRepository.Update(txCtx, toUpdatePaymentInput(p))
				return domain.ErrPaymentAmountMismatch
			}
		}

		// 4. State Machine Transition Validation
		var targetStatus domain.PaymentStatus
		switch input.EventType {
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
			paymentService.logger.Warn("rejected illegal payment state transition", "current", p.Status, "target", targetStatus, "payment_id", p.ID)
			return err
		}

		p.Status = targetStatus
		if p.Provider == "" {
			p.Provider = input.Provider
		}
		if p.ExternalID == "" {
			p.ExternalID = input.ExternalSessionID
		}

		if err := paymentService.paymentRepository.Update(txCtx, toUpdatePaymentInput(p)); err != nil {
			return err
		}

		var cancelled []CancelledAttemptOutput

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
			if err := paymentService.outboxRepository.SaveOutboxEvent(txCtx, outboxEvt.EventID, domain.RoutingKeyPaymentSucceeded, outboxEvt); err != nil {
				return err
			}

			// 6. Mark other attempts CANCELLED in DB
			attempts, _ := paymentService.paymentRepository.FindAttemptsByPaymentID(txCtx, p.ID)
			for _, att := range attempts {
				if att.Provider != p.Provider && att.ExternalSessionID != "" && att.Status != domain.AttemptStatusCancelled {
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
					_ = paymentService.paymentRepository.UpdateAttempt(txCtx, attUpdate)

					cancelled = append(cancelled, CancelledAttemptOutput{
						Provider:          att.Provider,
						ExternalSessionID: att.ExternalSessionID,
					})
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
				WebhookPayload:    input.RawPayload,
			}
			if err := paymentService.outboxRepository.SaveOutboxEvent(txCtx, lateEvt.EventID, domain.RoutingKeyPaymentLateReceived, lateEvt); err != nil {
				return err
			}

		case domain.PaymentStatusFailed:
			failedEvt := &domain.PaymentFailedEvent{
				EventID:   uuid.New().String(),
				TenantID:  p.TenantID,
				PaymentID: p.ID,
				OrderID:   p.OrderID,
				Reason:    "Webhook reported payment failure",
				Provider:  p.Provider,
			}
			if err := paymentService.outboxRepository.SaveOutboxEvent(txCtx, failedEvt.EventID, domain.RoutingKeyPaymentFailed, failedEvt); err != nil {
				return err
			}
		}

		output = &ProcessWebhookOutput{
			PaymentID:         p.ID,
			Status:            p.Status,
			CancelledAttempts: cancelled,
		}

		return nil
	}

	var err error
	if paymentService.txManager != nil {
		err = paymentService.txManager.WithTransaction(ctx, actionFn)
	} else {
		err = actionFn(ctx)
	}

	return output, err
}

func (paymentService *PaymentService) GetPaymentByID(ctx context.Context, id string) (*PaymentOutput, error) {
	p, err := paymentService.paymentRepository.FindByID(ctx, id)
	return toPaymentOutput(p), err
}

func (paymentService *PaymentService) GetPaymentByOrderID(ctx context.Context, tenantID, orderID string) (*PaymentOutput, error) {
	p, err := paymentService.paymentRepository.FindByOrderID(ctx, tenantID, orderID)
	return toPaymentOutput(p), err
}

func (paymentService *PaymentService) SweepExpiredPayments(ctx context.Context, ttlDuration time.Duration) (int, error) {
	expiredPayments, err := paymentService.paymentRepository.FindExpiredPayments(ctx, ttlDuration, 100)
	if err != nil || len(expiredPayments) == 0 {
		return 0, err
	}

	count := 0
	for _, p := range expiredPayments {
		processFn := func(txCtx context.Context) error {
			lockedPayment, err := paymentService.paymentRepository.FindByIDForUpdate(txCtx, p.ID)
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
			if err := paymentService.paymentRepository.Update(txCtx, toUpdatePaymentInput(lockedPayment)); err != nil {
				return err
			}

			expiredEvt := &domain.PaymentExpiredEvent{
				EventID:   uuid.New().String(),
				TenantID:  lockedPayment.TenantID,
				PaymentID: lockedPayment.ID,
				OrderID:   lockedPayment.OrderID,
				ExpiredAt: time.Now(),
			}

			return paymentService.outboxRepository.SaveOutboxEvent(txCtx, expiredEvt.EventID, domain.RoutingKeyPaymentExpired, expiredEvt)
		}

		var err error
		if paymentService.txManager != nil {
			err = paymentService.txManager.WithTransaction(ctx, processFn)
		} else {
			err = processFn(ctx)
		}

		if err == nil {
			count++
		}
	}

	return count, nil
}
