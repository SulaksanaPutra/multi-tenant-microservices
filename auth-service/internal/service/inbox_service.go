package service

import (
	"context"
	"fmt"

	"auth-service/internal/repository"
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
