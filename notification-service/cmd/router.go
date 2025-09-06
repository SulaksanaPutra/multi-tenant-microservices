package main

import (
	"net/http"
)

// newRouter initializes HTTP routes and health endpoints for Notification Service.
func newRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	return mux
}
