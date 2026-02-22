/*
 * Package e2e_test - E2E Testing Infrastructure & Shared Fixtures
 * File: auth_helpers_test.go
 *
 * Architectural Scope: Shared test setup, credential provisioning, JWT parsing, and polling helpers.
 * Objective: Provide robust, reusable, and deterministic test helper utilities for authenticating,
 *            provisioning users, parsing RSA keys, and polling background state changes across E2E tests.
 *
 * Core Utilities Provided:
 *   - setCredentials: Provisions initial password credentials via internal token flow.
 *   - loginAndGetToken / loginAndGetTokenPair: Authenticates user credentials against auth-service.
 *   - loadRSAPrivateKey: Resolves RSA private key for synthetic JWT generation in security tests.
 *   - waitForTenantActive: Deterministic database status polling.
 *   - resolveUserID: Safe user_id resolution fallback handling registration edge cases.
 */

package e2e_test

import (
	"bufio"
	"bytes"
	"crypto/rsa"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	authServiceURL = "http://localhost:8085"
	authLoginURL   = authServiceURL + "/auth/login"
	authRefreshURL = authServiceURL + "/auth/refresh"

	// userDBDSN is the local DSN for the user_db used as a fallback to resolve
	// user_id when the registration response does not include it.
	userDBDSN = "host=localhost port=5432 user=postgres password=postgres dbname=user_db sslmode=disable"
)

// defaultHTTPClient is the shared HTTP client used by all E2E tests.
// Using a named client (rather than http.DefaultClient) gives us an explicit
// timeout and avoids tests hanging indefinitely on unresponsive endpoints.
var defaultHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

// authTokenResponse maps the JSON envelope returned by POST /auth/login
// and POST /auth/refresh.
type authTokenResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	} `json:"data"`
}

// customJWTClaims mirrors the JWT payload structure issued by auth-service.
// Used by tests that need to construct synthetic tokens (e.g. TC-E2E-012,
// TC-E2E-016).
type customJWTClaims struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	jwt.RegisteredClaims
}

// --------------------------------------------------------------------------
// Auth Helpers
// --------------------------------------------------------------------------

// setCredentials provisions a password for a newly registered user via the
// internal setup-token endpoint and the POST /auth/credentials/setup flow.
//
// Instruction:
//   1. Request setup token from auth-service internal endpoint (/internal/auth/setup-token).
//   2. Retry up to 5 times with exponential backoff to handle asynchronous user creation races.
//   3. Submit password setup payload to POST /auth/credentials/setup.
//
// Architectural Invariant:
//   User must be fully provisioned in public.user_credentials before attempting login.
func setCredentials(t *testing.T, userID, tenantID, email, password string) {
	t.Helper()

	tokenReqBody, _ := json.Marshal(map[string]string{
		"user_id":   userID,
		"tenant_id": tenantID,
		"email":     email,
	})

	var rawSetupToken string
	var lastErr error

	// Retry loop for internal setup token generation to handle user-service writing latency
	for i := 0; i < 5; i++ {
		req, err := http.NewRequest(http.MethodPost, authServiceURL+"/internal/auth/setup-token", bytes.NewBuffer(tokenReqBody))
		if err != nil {
			t.Fatalf("[Auth] Failed to create internal setup-token request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Service-Token", "default_internal_service_token")

		resp, err := defaultHTTPClient.Do(req)
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
		t.Fatalf("[Auth] setCredentials: failed to obtain setup token: %v", lastErr)
	}

	setupBody, _ := json.Marshal(map[string]string{
		"token":    rawSetupToken,
		"password": password,
	})

	resp, err := defaultHTTPClient.Post(authServiceURL+"/auth/credentials/setup", "application/json", bytes.NewBuffer(setupBody))
	if err != nil {
		t.Fatalf("[Auth] POST /auth/credentials/setup failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[Auth] setCredentials: POST /auth/credentials/setup returned status %d", resp.StatusCode)
	}

	t.Logf("[Auth] Credentials provisioned for email='%s' user_id='%s'", email, userID)
}

// loginAndGetToken authenticates via POST /auth/login and returns the short-lived JWT access token.
// Instruction: Calls loginAndGetTokenPair and extracts only the access token for convenience.
func loginAndGetToken(t *testing.T, email, password string) string {
	t.Helper()
	accessToken, _ := loginAndGetTokenPair(t, email, password)
	return accessToken
}

// loginAndGetTokenPair calls POST /auth/login and returns both the access token and refresh token.
// Instruction:
//   1. Submits POST /auth/login request with user email and password.
//   2. Decodes JSON token envelope and verifies presence of access_token and refresh_token.
func loginAndGetTokenPair(t *testing.T, email, password string) (accessToken, refreshToken string) {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})

	resp, err := defaultHTTPClient.Post(authLoginURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("[Auth] POST /auth/login failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[Auth] POST /auth/login returned status %d", resp.StatusCode)
	}

	var tokenResp authTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("[Auth] failed to decode login response: %v", err)
	}

	if tokenResp.Data.AccessToken == "" {
		t.Fatalf("[Auth] login response missing access_token")
	}
	if tokenResp.Data.RefreshToken == "" {
		t.Fatalf("[Auth] login response missing refresh_token")
	}

	t.Logf("[Auth] Login successful for email='%s'", email)
	return tokenResp.Data.AccessToken, tokenResp.Data.RefreshToken
}

// bearerHeader formats a standard HTTP Authorization bearer header string.
func bearerHeader(token string) string {
	return "Bearer " + token
}

// --------------------------------------------------------------------------
// JWT Key Helpers
// --------------------------------------------------------------------------

// loadRSAPrivateKey resolves the RSA private key used by auth-service.
// Instruction:
//   1. Checks AUTH_JWT_PRIVATE_KEY_PEM env variable.
//   2. If empty, inspects nearby .env files (auth-service/.env, .env).
//   3. Parses PEM string into *rsa.PrivateKey.
//
// Architectural Invariant:
//   Required for security E2E tests (e.g. TC-E2E-012 expired token simulation).
func loadRSAPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	pemStr := os.Getenv("AUTH_JWT_PRIVATE_KEY_PEM")

	if pemStr == "" {
		paths := []string{
			"../auth-service/.env",
			"../.env",
			"./.env",
		}
		for _, p := range paths {
			absPath, err := filepath.Abs(p)
			if err != nil {
				continue
			}
			f, err := os.Open(absPath)
			if err != nil {
				continue
			}
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 && strings.TrimSpace(parts[0]) == "AUTH_JWT_PRIVATE_KEY_PEM" {
					val := strings.TrimSpace(parts[1])
					val = strings.Trim(val, `"'`)
					val = strings.ReplaceAll(val, `\n`, "\n")
					pemStr = val
					break
				}
			}
			f.Close()
			if pemStr != "" {
				break
			}
		}
	}

	if pemStr == "" {
		t.Fatalf("[Auth] AUTH_JWT_PRIVATE_KEY_PEM is not set and could not be loaded from any .env file")
	}

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pemStr))
	if err != nil {
		t.Fatalf("[Auth] Failed to parse RSA private key PEM: %v", err)
	}
	return privateKey
}

// --------------------------------------------------------------------------
// Polling & Resolution Helpers
// --------------------------------------------------------------------------

// waitForTenantActive polls tenant_manager_db until the specified tenant reaches 'active' status.
// Instruction: Polls public.tenants every 500ms for up to 15 seconds before timing out.
func waitForTenantActive(t *testing.T, db *sql.DB, tenantID string) {
	t.Helper()

	for i := 0; i < 30; i++ {
		var status string
		err := db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&status)
		if err == nil && status == "active" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("[Setup] Tenant '%s' did not reach 'active' status within 15 seconds", tenantID)
}

// resolveUserID resolves the user ID for a tenant owner.
// Instruction: Returns regResp.Data.UserID directly if present; otherwise queries user_db by email.
func resolveUserID(t *testing.T, regResp RegisterResp, ownerEmail string) string {
	t.Helper()

	if regResp.Data.UserID != "" {
		return regResp.Data.UserID
	}

	t.Logf("[Setup] user_id not in registration response — querying user_db for email='%s'", ownerEmail)

	userDB, err := sql.Open("postgres", userDBDSN)
	if err != nil {
		t.Fatalf("[Setup] Failed to open user_db connection: %v", err)
	}
	defer userDB.Close()

	var userID string
	for i := 0; i < 20; i++ {
		err := userDB.QueryRow("SELECT id FROM users WHERE email = $1", ownerEmail).Scan(&userID)
		if err == nil && userID != "" {
			return userID
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("[Setup] Could not resolve user_id for email='%s' within 10 seconds", ownerEmail)
	return ""
}
