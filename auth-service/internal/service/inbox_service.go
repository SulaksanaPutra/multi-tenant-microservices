package service

import (
	"context"
	"fmt"

	"auth-service/internal/repository"
)

// InboxRepository is the consumer-side interface expected by InboxService.
type InboxRepository interface {
	TryInsert(ctx context.Context, input repository.CreateInboxMessageInput) (bool, error)
}

type InboxService struct {
	inboxRepository InboxRepository
}

func NewInboxService(inboxRepository InboxRepository) *InboxService {
	return &InboxService{
		inboxRepository: inboxRepository,
	}
}

// ClaimEvent participates in the outer Unit-of-Work passed via txCtx.
// It attempts to claim the event_id in the inbox repository and returns
// (isDuplicate, error).
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
