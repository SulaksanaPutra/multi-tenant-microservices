package main

import (
	"net/http"

	"tenant-service/internal/handler"
	"tenant-service/internal/middleware"
	"tenant-service/internal/repository"
	"tenant-service/internal/service"
	"tenant-service/internal/types"
)

// newRouter initializes all HTTP routes, middleware, and handler factories.
func newRouter(tenantMiddleware *middleware.TenantMiddleware) http.Handler {
	// Handler Factory for Member API
	memberHandlerFactory := func(cfg types.TenantConfig) http.HandlerFunc {
		memberRepo := repository.NewMemberRepository(cfg)
		memberService := service.NewMemberService(memberRepo)
		memberHandler := handler.NewMemberHandler(memberService)
		return memberHandler.GetMembers
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/members", tenantMiddleware.ResolveTenant(memberHandlerFactory))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	return mux
}
