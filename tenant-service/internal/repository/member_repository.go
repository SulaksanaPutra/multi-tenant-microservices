package repository

import (
	"context"
	"fmt"
	"time"

	"tenant-service/internal/txctx"
	"tenant-service/internal/types"
)

type Member struct {
	ID       int
	UserID   string
	Name     string
	Email    string
	Role     string
	JoinedAt time.Time
}

// MemberRepository handles member queries scoped to a specific tenant's database connection and schema.
type MemberRepository interface {
	GetMembers(ctx context.Context) ([]Member, error)
}

type memberRepository struct {
	cfg types.TenantConfig
}

// NewMemberRepository constructs a repository instance bound to the resolved TenantConfig.
func NewMemberRepository(cfg types.TenantConfig) MemberRepository {
	return &memberRepository{
		cfg: cfg,
	}
}

func (r *memberRepository) GetMembers(ctx context.Context) ([]Member, error) {
	exec := txctx.GetExecutor(ctx, r.cfg.DB)
	query := fmt.Sprintf(`
		SELECT id, user_id, name, email, role, joined_at
		FROM %s.tenant_members
		ORDER BY id ASC;
	`, r.cfg.TargetSchema)

	rows, err := exec.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query tenant_members from schema '%s': %w", r.cfg.TargetSchema, err)
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.UserID, &m.Name, &m.Email, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("failed to scan tenant_member row: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
