package handler

import (
	"net/http"
	"time"

	"tenant-service/internal/service"
)

type MemberResponse struct {
	ID       int       `json:"id"`
	UserID   string    `json:"user_id"`
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type GetMembersResponse struct {
	Members []MemberResponse `json:"members"`
}

type MemberHandler struct {
	memberService service.MemberService
}

func NewMemberHandler(memberService service.MemberService) *MemberHandler {
	return &MemberHandler{
		memberService: memberService,
	}
}

func (h *MemberHandler) GetMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	output, err := h.memberService.GetMembers(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	members := make([]MemberResponse, len(output.Members))
	for i, m := range output.Members {
		members[i] = MemberResponse{
			ID:       m.ID,
			UserID:   m.UserID,
			Name:     m.Name,
			Email:    m.Email,
			Role:     m.Role,
			JoinedAt: m.JoinedAt,
		}
	}

	resp := GetMembersResponse{
		Members: members,
	}

	WriteJSON(w, http.StatusOK, resp)
}
