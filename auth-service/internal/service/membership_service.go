package service

import (
	"context"
	"fmt"
)

// MembershipRepository is the consumer-side interface expected by MembershipService.
type MembershipRepository interface {
	AddMembership(ctx context.Context, userID, tenantID string) error
}

type MembershipService struct {
	membershipRepository MembershipRepository
}

func NewMembershipService(membershipRepository MembershipRepository) *MembershipService {
	return &MembershipService{
		membershipRepository: membershipRepository,
	}
}

// AddMembership persists a user's tenant membership. It participates in the
// outer Unit-of-Work passed via txCtx when invoked from a consumer transaction.
func (membershipService *MembershipService) AddMembership(ctx context.Context, userID, tenantID string) error {
	if err := membershipService.membershipRepository.AddMembership(ctx, userID, tenantID); err != nil {
		return fmt.Errorf("membership service: failed to add user_id='%s' to tenant_id='%s': %w", userID, tenantID, err)
	}
	return nil
}
