package authclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPermissionRegistrar_Constructor(t *testing.T) {
	registrar := NewPermissionRegistrar("", "")
	if registrar == nil {
		t.Fatal("expected non-nil PermissionRegistrar")
	}
	if registrar.authServiceURL != "http://auth-service:8085" {
		t.Errorf("expected default authServiceURL, got %s", registrar.authServiceURL)
	}
	if registrar.internalServiceToken != "default_internal_service_token" {
		t.Errorf("expected default internalServiceToken, got %s", registrar.internalServiceToken)
	}

	custom := NewPermissionRegistrar("http://custom:9000", "custom_token")
	if custom.authServiceURL != "http://custom:9000" || custom.internalServiceToken != "custom_token" {
		t.Errorf("unexpected custom registrar configuration: %+v", custom)
	}
}

func TestPermissionRegistrar_Register_SuccessAndIdempotency(t *testing.T) {
	var callCount atomic.Int32
	var capturedPayload registerPayload
	var capturedToken string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		capturedToken = r.Header.Get("X-Internal-Service-Token")
		_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	registrar := NewPermissionRegistrar(ts.URL, "test_secret_token")
	ctx := context.Background()

	perms := []PermissionItem{
		{Name: "users:read", Description: "Read user profiles"},
		{Name: "users:write", Description: "Update user profiles"},
	}

	err := registrar.Register(ctx, "user-service", perms)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if callCount.Load() != 1 {
		t.Errorf("expected 1 HTTP call, got %d", callCount.Load())
	}
	if capturedToken != "test_secret_token" {
		t.Errorf("expected token 'test_secret_token', got '%s'", capturedToken)
	}
	if capturedPayload.Service != "user-service" || len(capturedPayload.Permissions) != 2 {
		t.Errorf("unexpected payload captured: %+v", capturedPayload)
	}

	// Test Idempotency: Second call should return immediately without making HTTP request
	err = registrar.Register(ctx, "user-service", perms)
	if err != nil {
		t.Fatalf("expected nil error on second call, got %v", err)
	}
	if callCount.Load() != 1 {
		t.Errorf("expected callCount to remain 1 due to idempotency, got %d", callCount.Load())
	}
}

func TestPermissionRegistrar_Register_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	registrar := NewPermissionRegistrar(ts.URL, "token")

	// Pass a context with short timeout to prevent waiting through all 10 retry attempts
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := registrar.Register(ctx, "user-service", []PermissionItem{{Name: "users:read"}})
	if err == nil {
		t.Fatal("expected error on server error response, got nil")
	}
}
