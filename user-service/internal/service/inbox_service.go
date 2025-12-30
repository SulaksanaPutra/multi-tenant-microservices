package service

import (
	"context"
	"fmt"
)

// InboxRepository is the consumer-side interface expected by InboxService.
type InboxRepository interface {
	TryInsert(ctx context.Context, eventID string) (bool, error)
}

type InboxService struct {
	inboxRepo InboxRepository
}

func NewInboxService(inboxRepo InboxRepository) *InboxService {
	return &InboxService{
		inboxRepo: inboxRepo,
	}
}

// ClaimEvent participates in the Outer Unit-of-Work passed via txCtx.
// It attempts to claim the event_id in the inbox repository and returns (isDuplicate, error).
func (s *InboxService) ClaimEvent(txCtx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	isDuplicate, err := s.inboxRepo.TryInsert(txCtx, eventID)
	if err != nil {
		return false, fmt.Errorf("inbox service: failed to claim event_id='%s': %w", eventID, err)
	}
	return isDuplicate, nil
}
