package service

import (
	"context"
	"fmt"

	"notification-service/internal/domain"
	"notification-service/internal/repository"
)

// InboxServiceRepository is the consumer-side interface expected by InboxService.
type InboxServiceRepository interface {
	TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
	GetEventsByTenantID(ctx context.Context, tenantID string) ([]domain.InboxMessage, error)
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
// It attempts to atomically insert the event into the inbox table.
// Returns (isDuplicate=true, nil) if the event_id was already processed — caller should skip cleanly.
// Returns (false, nil) if the event is new and safe to process.
// Returns (false, err) on infrastructure failure — caller should NACK for retry.
func (s *InboxService) ClaimEvent(txCtx context.Context, input repository.CreateInboxMessageInput) (bool, error) {
	if input.EventID == "" {
		return false, nil
	}
	isDuplicate, err := s.inboxRepository.TryInsert(txCtx, input)
	if err != nil {
		return false, fmt.Errorf("inbox service: failed to claim event_id='%s': %w", input.EventID, err)
	}
	return isDuplicate, nil
}

// GetBarrierEvents returns all inbox events recorded for the given tenant.
// Called at Layer 1 by consumers after ClaimEvent succeeds, so that the resulting
// []domain.InboxMessage slice can be passed as data into the business service.
// This participates in the Outer Unit-of-Work via txCtx, ensuring the read is
// consistent with the just-inserted ClaimEvent row within the same transaction.
func (s *InboxService) GetBarrierEvents(txCtx context.Context, tenantID string) ([]domain.InboxMessage, error) {
	events, err := s.inboxRepository.GetEventsByTenantID(txCtx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("inbox service: failed to fetch barrier events for tenant_id='%s': %w", tenantID, err)
	}
	return events, nil
}
