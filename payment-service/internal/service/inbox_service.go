package service

import (
	"context"
	"fmt"

	"payment-service/internal/repository"
)

// ClaimInboxInput is the Layer-2 service DTO used by Layer-1 consumers when
// claiming an inbound event through the transactional inbox guard.
type ClaimInboxInput struct {
	EventID   string
	TenantID  string
	EventType string
	Payload   []byte
}

// InboxServiceRepository is the consumer-side interface expected by InboxService.
type InboxServiceRepository interface {
	TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
}

type InboxService struct {
	inboxRepository InboxServiceRepository
}

func NewInboxService(inboxRepository InboxServiceRepository) *InboxService {
	return &InboxService{
		inboxRepository: inboxRepository,
	}
}

// ClaimEvent participates in the outer Unit-of-Work passed via txCtx.
// It attempts to claim the event_id in the inbox repository and returns
// (isDuplicate, error).
func (s *InboxService) ClaimEvent(txCtx context.Context, input ClaimInboxInput) (bool, error) {
	if input.EventID == "" {
		return false, nil
	}
	isDuplicate, err := s.inboxRepository.TryInsert(txCtx, repository.CreateInboxMessageInput{
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
