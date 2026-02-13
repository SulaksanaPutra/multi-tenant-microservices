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

// setCredentials provisions a user's password via the internal setup token endpoint and setup password flow.
func setCredentials(t *testing.T, userID, tenantID, email, password string) {
	t.Helper()

	tokenReqBody, _ := json.Marshal(map[string]string{
		"user_id":   userID,
		"tenant_id": tenantID,
		"email":     email,
	})

	var rawSetupToken string
	var lastErr error

	// 1. Fetch setup token via internal API
	for i := 0; i < 5; i++ {
		req, err := http.NewRequest(http.MethodPost, authServiceURL+"/internal/auth/setup-token", bytes.NewBuffer(tokenReqBody))
		if err != nil {
			t.Fatalf("Failed to create request for internal setup-token: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Service-Token", "default_internal_service_token")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var tokenResp struct {
				Data struct {
					Token string `json:"token"`
				} `json:"data"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err == nil && tokenResp.Data.Token != "" {
				rawSetupToken = tokenResp.Data.Token
				break
			}
		}
		lastErr = fmt.Errorf("internal setup-token returned status %d", resp.StatusCode)
		time.Sleep(time.Second)
	}

	if rawSetupToken == "" {
		t.Fatalf("setCredentials failed to fetch setup token: %v", lastErr)
	}

	// 2. Submit password setup request
	setupReqBody, _ := json.Marshal(map[string]string{
		"token":    rawSetupToken,
		"password": password,
	})

	resp, err := http.Post(authServiceURL+"/auth/credentials/setup", "application/json", bytes.NewBuffer(setupReqBody))
	if err != nil {
		t.Fatalf("POST /auth/credentials/setup failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setCredentials setup password returned status %d", resp.StatusCode)
	}

	t.Logf("[Auth] Credentials set successfully for email='%s' user_id='%s'", email, userID)
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
