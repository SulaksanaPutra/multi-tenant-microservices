package service

import (
	"context"
	"fmt"

	"payment-service/internal/repository"
)

type ClaimInboxInput struct {
	EventID   string
	TenantID  string
	EventType string
	Payload   []byte
}

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

func (inboxService *InboxService) ClaimEvent(txCtx context.Context, input ClaimInboxInput) (bool, error) {
	if input.EventID == "" {
		return false, nil
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
