package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const (
	authServiceURL    = "http://localhost:8085"
	authCredSetURL    = authServiceURL + "/auth/credentials/set"
	authLoginURL      = authServiceURL + "/auth/login"
	authRefreshURL    = authServiceURL + "/auth/refresh"
)

// authTokenResponse maps the JSON response from POST /auth/login and POST /auth/refresh.
type authTokenResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	} `json:"data"`
}

// setCredentials calls the TEMPORARY credential provisioning endpoint to assign a password
// to a registered user. This is Stage 1 scaffolding only.
//
// TEMPORARY — NON-PRODUCTION SCAFFOLDING.
// Will be replaced by an email-invite / token-gated reset flow in the OAuth 2.0 stage.
func setCredentials(t *testing.T, userID, tenantID, email, password string) {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"user_id":   userID,
		"tenant_id": tenantID,
		"email":     email,
		"password":  password,
	})

	// Retry up to 5 times — auth-service may not be fully up yet right after startup
	var lastErr error
	for i := 0; i < 5; i++ {
		resp, err := http.Post(authCredSetURL, "application/json", bytes.NewBuffer(body))
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Logf("[Auth] Credentials set for email='%s' user_id='%s'", email, userID)
			return
		}
		lastErr = fmt.Errorf("setCredentials returned status %d", resp.StatusCode)
		time.Sleep(time.Second)
	}
	t.Fatalf("setCredentials failed after retries: %v", lastErr)
}

// loginAndGetToken calls POST /auth/login and returns the JWT access token.
func loginAndGetToken(t *testing.T, email, password string) string {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})

	resp, err := http.Post(authLoginURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("POST /auth/login failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /auth/login returned status %d", resp.StatusCode)
	}

	var tokenResp authTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}

	if tokenResp.Data.AccessToken == "" {
		t.Fatalf("login response missing access_token")
	}

	t.Logf("[Auth] Login successful for email='%s'", email)
	return tokenResp.Data.AccessToken
}

// bearerHeader returns the Authorization: Bearer header value for a given token.
func bearerHeader(token string) string {
	return "Bearer " + token
}
