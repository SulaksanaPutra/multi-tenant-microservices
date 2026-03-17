package authclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthClient_Constructor(t *testing.T) {
	client := NewAuthClient("", "")
	if client == nil {
		t.Fatal("expected non-nil AuthClient")
	}
	if client.authServiceURL != "http://auth-service:8085" {
		t.Errorf("expected default authServiceURL, got %s", client.authServiceURL)
	}
	if client.internalToken != "default_internal_service_token" {
		t.Errorf("expected default internalToken, got %s", client.internalToken)
	}

	custom := NewAuthClient("http://custom:9000", "custom_secret")
	if custom.authServiceURL != "http://custom:9000" || custom.internalToken != "custom_secret" {
		t.Errorf("unexpected custom client configuration: %+v", custom)
	}
}

func TestAuthClient_FetchSetupToken_Success(t *testing.T) {
	var capturedTokenHeader string
	var capturedReq createSetupTokenRequest

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTokenHeader = r.Header.Get("X-Internal-Service-Token")
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)

		resp := createSetupTokenResponse{
			Status:  "success",
			Message: "Setup token created",
		}
		resp.Data.Token = "st_test_setup_token_123"

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := NewAuthClient(ts.URL, "secret_internal_token")
	ctx := context.Background()

	token, err := client.FetchSetupToken(ctx, "usr_100", "tnt_200", "user@example.com")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if token != "st_test_setup_token_123" {
		t.Errorf("expected token 'st_test_setup_token_123', got '%s'", token)
	}
	if capturedTokenHeader != "secret_internal_token" {
		t.Errorf("expected header 'secret_internal_token', got '%s'", capturedTokenHeader)
	}
	if capturedReq.UserID != "usr_100" || capturedReq.TenantID != "tnt_200" || capturedReq.Email != "user@example.com" {
		t.Errorf("unexpected request payload: %+v", capturedReq)
	}
}

func TestAuthClient_FetchSetupToken_Errors(t *testing.T) {
	t.Run("server error status", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		client := NewAuthClient(ts.URL, "token")
		_, err := client.FetchSetupToken(context.Background(), "u1", "t1", "e1")
		if err == nil {
			t.Fatal("expected error on 500 status code, got nil")
		}
	})

	t.Run("empty setup token in response", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := createSetupTokenResponse{Status: "success"}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer ts.Close()

		client := NewAuthClient(ts.URL, "token")
		_, err := client.FetchSetupToken(context.Background(), "u1", "t1", "e1")
		if err == nil {
			t.Fatal("expected error on empty token in response, got nil")
		}
	})
}
