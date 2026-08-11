package authclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewPermissionRegistrar_Defaults(t *testing.T) {
	r := NewPermissionRegistrar("", "")
	if r.authServiceURL != "http://auth-service:8085" {
		t.Errorf("expected default auth-service URL, got %q", r.authServiceURL)
	}
	if r.internalServiceToken != "default_internal_service_token" {
		t.Errorf("expected default internal token, got %q", r.internalServiceToken)
	}
	if cap(r.httpSemaphore) != 50 {
		t.Errorf("expected semaphore capacity 50, got %d", cap(r.httpSemaphore))
	}
}

func TestPermissionRegistrar_Register(t *testing.T) {
	var gotPath string
	var gotToken string
	var gotBody string
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		gotPath = req.URL.Path
		gotToken = req.Header.Get("X-Internal-Service-Token")
		buf := make([]byte, 4096)
		n, _ := req.Body.Read(buf)
		gotBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := NewPermissionRegistrar(srv.URL, "tok_123")
	err := r.Register(context.Background(), "payment-service", []PermissionItem{
		{Name: "payments:read", Description: "Read payments"},
	})
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if gotPath != "/internal/auth/permissions/register" {
		t.Errorf("expected register path, got %q", gotPath)
	}
	if gotToken != "tok_123" {
		t.Errorf("expected internal token header, got %q", gotToken)
	}
	var payload registerPayload
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}
	if payload.Service != "payment-service" {
		t.Errorf("expected service name 'payment-service', got %q", payload.Service)
	}
	if len(payload.Permissions) != 1 || payload.Permissions[0].Name != "payments:read" {
		t.Errorf("unexpected permissions payload: %+v", payload.Permissions)
	}

	// A second Register call short-circuits on the registered flag.
	if err := r.Register(context.Background(), "payment-service", nil); err != nil {
		t.Fatalf("second Register failed: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 HTTP call after registered, got %d", calls.Load())
	}
}

func TestPermissionRegistrar_doRegister_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := NewPermissionRegistrar(srv.URL, "tok")
	err := r.doRegister(context.Background(), "payment-service", []PermissionItem{{Name: "x"}})
	if err == nil {
		t.Fatal("expected error on non-200 response")
	}
	if !strings.Contains(err.Error(), "auth-service returned status") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestPermissionRegistrar_doRegister_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := NewPermissionRegistrar("http://example.com", "tok")
	err := r.doRegister(ctx, "payment-service", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
