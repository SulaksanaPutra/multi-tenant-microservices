package service

import (
	"context"
	"fmt"
)

type InboxRepository interface {
	TryInsert(ctx context.Context, eventID string) (bool, error)
}

type InboxService struct {
	inboxRepository InboxRepository
}

func NewInboxService(inboxRepository InboxRepository) *InboxService {
	return &InboxService{
		inboxRepository: inboxRepository,
	}
}

func (inboxService *InboxService) ClaimEvent(txCtx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	isDuplicate, err := inboxService.inboxRepository.TryInsert(txCtx, eventID)
	if err != nil {
		return false, fmt.Errorf("inbox service: failed to claim event_id='%s': %w", eventID, err)
	}
	return isDuplicate, nil
}
