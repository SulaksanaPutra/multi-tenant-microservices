package main

import (
	"net/http"

	"user-service/internal/handler"
)

func newRouter(userHandler *handler.UserHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/register", userHandler.RegisterUser)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	return mux
}
