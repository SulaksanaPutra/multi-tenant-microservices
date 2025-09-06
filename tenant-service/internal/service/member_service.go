package service

import (
	"context"
	"fmt"

	"tenant-service/internal/repository"
)

type GetMembersOutput struct {
	Members []repository.Member
}

type MemberService interface {
	GetMembers(ctx context.Context) (*GetMembersOutput, error)
}

type memberService struct {
	repo repository.MemberRepository
}

func NewMemberService(repo repository.MemberRepository) MemberService {
	return &memberService{
		repo: repo,
	}
}

func (s *memberService) GetMembers(ctx context.Context) (*GetMembersOutput, error) {
	members, err := s.repo.GetMembers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tenant members: %w", err)
	}

	return &GetMembersOutput{
		Members: members,
	}, nil
}
