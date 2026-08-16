package service

import (
	"context"
	"fmt"

	"notification-service/internal/domain"
	"notification-service/internal/repository"
)

// ClaimInboxInput is the Layer-2 service DTO used by Layer-1 consumers when
// claiming an inbound event through the transactional inbox guard. It keeps
// repository DTOs out of the consumer/handler layer.
type ClaimInboxInput struct {
	EventID   string
	TenantID  string
	EventType string
	Payload   []byte
}

// InboxServiceRepository is the consumer-side interface expected by InboxService.
type InboxServiceRepository interface {
	TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
	ListEventsByTenantID(ctx context.Context, tenantID string) ([]domain.InboxMessage, error)
	AcquireTenantLock(ctx context.Context, tenantID string) error
}

type InboxService struct {
	inboxRepository InboxServiceRepository
}

func NewInboxService(inboxRepository InboxServiceRepository) *InboxService {
	return &InboxService{
		inboxRepository: inboxRepository,
	}
}

// ClaimEvent participates in the Outer Unit-of-Work passed via txCtx.
// It acquires a transactional advisory lock on tenantID (to serialize barrier checks across concurrent transactions/replicas)
// and attempts to atomically insert the event into the inbox table.
// Returns (isDuplicate=true, nil) if the event_id was already processed — caller should skip cleanly.
// Returns (false, nil) if the event is new and safe to process.
// Returns (false, err) on infrastructure failure — caller should NACK for retry.
func (inboxService *InboxService) ClaimEvent(txCtx context.Context, input ClaimInboxInput) (bool, error) {
	if input.EventID == "" {
		return false, nil
	}
	if input.TenantID != "" {
		if err := inboxService.inboxRepository.AcquireTenantLock(txCtx, input.TenantID); err != nil {
			return false, fmt.Errorf("inbox service: failed to acquire tenant lock for tenant_id='%s': %w", input.TenantID, err)
		}
	}
	isDuplicate, err := inboxService.inboxRepository.TryInsert(txCtx, repository.CreateInboxMessageInput{
		EventID:   input.EventID,
		TenantID:  input.TenantID,
		EventType: input.EventType,
		Payload:   input.Payload,
	})
	if err != nil {
		return false, fmt.Errorf("inbox service: failed to claim event_id='%s': %w", input.EventID, err)
	}
	return isDuplicate, nil
}

// ListBarrierEvents returns all inbox events recorded for the given tenant.
func (inboxService *InboxService) ListBarrierEvents(txCtx context.Context, tenantID string) ([]domain.InboxMessage, error) {
	events, err := inboxService.inboxRepository.ListEventsByTenantID(txCtx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("inbox service: failed to fetch barrier events for tenant_id='%s': %w", tenantID, err)
	}
	return events, nil
}
