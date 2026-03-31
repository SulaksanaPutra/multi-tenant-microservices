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
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	gatewayBaseURL = "http://localhost:8000"
	authServiceURL = "http://localhost:8085"
	authLoginURL   = authServiceURL + "/api/auth/login"
	authRefreshURL = authServiceURL + "/api/auth/refresh"

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
// internal setup-token endpoint and the POST /api/auth/credentials/setup flow.
//
// Instruction:
//   1. Request setup token from auth-service internal endpoint (/internal/auth/setup-token).
//   2. Retry up to 5 times with exponential backoff to handle asynchronous user creation races.
//   3. Submit password setup payload to POST /api/auth/credentials/setup.
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

	resp, err := defaultHTTPClient.Post(authServiceURL+"/api/auth/credentials/setup", "application/json", bytes.NewBuffer(setupBody))
	if err != nil {
		t.Fatalf("[Auth] POST /api/auth/credentials/setup failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[Auth] setCredentials: POST /api/auth/credentials/setup returned status %d", resp.StatusCode)
	}

	t.Logf("[Auth] Credentials provisioned for email='%s' user_id='%s'", email, userID)
}

// loginAndGetToken authenticates via POST /api/auth/login and returns the short-lived JWT access token.
// Instruction: Calls loginAndGetTokenPair and extracts only the access token for convenience.
func loginAndGetToken(t *testing.T, email, password string) string {
	t.Helper()
	accessToken, _ := loginAndGetTokenPair(t, email, password)
	return accessToken
}

func loginAndGetTokenPair(t *testing.T, email, password string) (accessToken, refreshToken string) {
	return loginAndGetTokenWithTenant(t, "", email, password)
}

// loginAndGetTokenWithTenant authenticates via POST /api/auth/login and resolves the
// workspace selection flow when the account belongs to multiple tenants.
func loginAndGetTokenWithTenant(t *testing.T, tenantID, email, password string) (accessToken, refreshToken string) {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})

	resp, err := defaultHTTPClient.Post(authLoginURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("[Auth] POST /api/auth/login failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[Auth] POST /api/auth/login returned status %d", resp.StatusCode)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("[Auth] failed to decode login response: %v", err)
	}

	dataMap, _ := raw["data"].(map[string]any)
	if dataMap == nil {
		t.Fatalf("[Auth] login response missing data object: %v", raw)
	}

	if status, ok := dataMap["status"].(string); ok && status == "SELECT_WORKSPACE" {
		exchangeToken, _ := dataMap["exchange_token"].(string)
		workspaces, _ := dataMap["workspaces"].([]any)

		targetTenantID := tenantID
		if targetTenantID == "" && len(workspaces) > 0 {
			if ws, ok := workspaces[0].(map[string]any); ok {
				targetTenantID, _ = ws["tenant_id"].(string)
			}
		}

		selectBody, _ := json.Marshal(map[string]string{
			"exchange_token": exchangeToken,
			"tenant_id":      targetTenantID,
		})

		selectResp, err := defaultHTTPClient.Post(authServiceURL+"/api/auth/select-tenant", "application/json", bytes.NewBuffer(selectBody))
		if err != nil {
			t.Fatalf("[Auth] POST /api/auth/select-tenant failed: %v", err)
		}
		defer selectResp.Body.Close()

		if selectResp.StatusCode != http.StatusOK {
			t.Fatalf("[Auth] POST /api/auth/select-tenant returned status %d", selectResp.StatusCode)
		}

		var selectTokenResp authTokenResponse
		if err := json.NewDecoder(selectResp.Body).Decode(&selectTokenResp); err != nil {
			t.Fatalf("[Auth] failed to decode select-tenant response: %v", err)
		}
		return selectTokenResp.Data.AccessToken, selectTokenResp.Data.RefreshToken
	}

	at, _ := dataMap["access_token"].(string)
	rt, _ := dataMap["refresh_token"].(string)
	return at, rt
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

// resolveTenantID resolves the tenant ID for a given owner email from tenant_manager_db.
func resolveTenantID(t *testing.T, ownerEmail string) string {
	t.Helper()

	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("[Setup] Failed to open tenant_manager_db connection: %v", err)
	}
	defer db.Close()

	var tenantID string
	for i := 0; i < 20; i++ {
		err := db.QueryRow("SELECT id FROM public.tenants WHERE owner_email = $1", ownerEmail).Scan(&tenantID)
		if err == nil && tenantID != "" {
			return tenantID
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("[Setup] Could not resolve tenant_id for email='%s' within 10 seconds", ownerEmail)
	return ""
}

// registerAndActivateTenant executes the asynchronous tenant registration flow:
// 1. Connects to RabbitMQ using rabbitmqDSN.
// 2. Binds an ephemeral queue to company.events exchange on workspace.initiated routing key.
// 3. Submits HTTP POST /api/tenants/register request using generateFakeData.
// 4. Waits for the workspace.initiated event on RabbitMQ (10s timeout) and extracts tenant_id.
// 5. Polls tenant_manager_db using waitForTenantActive until status is 'active'.
// 6. Resolves user_id using resolveUserID.
// 7. Returns tenantID, userID, ownerEmail, and a generated password.
func registerAndActivateTenant(t *testing.T, plan ...string) (tenantID, userID, ownerEmail, password string) {
	t.Helper()

	planName := "shared"
	if len(plan) > 0 && plan[0] != "" {
		planName = plan[0]
	}

	// 1. Connect to RabbitMQ
	rmqConn, err := amqp.Dial(rabbitmqDSN)
	if err != nil {
		t.Fatalf("[Setup] Failed to connect to RabbitMQ: %v", err)
	}
	defer rmqConn.Close()

	ch, err := rmqConn.Channel()
	if err != nil {
		t.Fatalf("[Setup] Failed to open RabbitMQ channel: %v", err)
	}
	defer ch.Close()

	// 2. Declare ephemeral queue bound to company.events on workspace.initiated
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("[Setup] Failed to declare RabbitMQ queue: %v", err)
	}
	if err := ch.QueueBind(q.Name, "workspace.initiated", "company.events", false, nil); err != nil {
		t.Fatalf("[Setup] Failed to bind RabbitMQ queue: %v", err)
	}

	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("[Setup] Failed to consume from RabbitMQ queue: %v", err)
	}

	// 3. Submit HTTP POST /api/tenants/register
	ownerName, ownerEmail, tenantName, _ := generateFakeData(planName)
	t.Logf("[Setup] Submitting Registration: owner='%s', email='%s', tenant='%s', plan='%s'", ownerName, ownerEmail, tenantName, planName)

	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       planName,
		TenantName: tenantName,
	})

	resp, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("[Setup] HTTP POST /api/tenants/register failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("[Setup] Expected HTTP 202 Accepted, got %d", resp.StatusCode)
	}

	var regResp RegisterResp
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("[Setup] Failed to decode register response: %v", err)
	}

	// 4. Wait for workspace.initiated event on RabbitMQ queue (10s timeout) and extract tenant_id
	select {
	case d := <-msgs:
		var event map[string]any
		if err := json.Unmarshal(d.Body, &event); err != nil {
			t.Fatalf("[Setup] Failed to unmarshal RabbitMQ event payload: %v", err)
		}
		tID, ok := event["tenant_id"].(string)
		if !ok || tID == "" {
			t.Fatalf("[Setup] RabbitMQ event missing tenant_id: %v", event)
		}
		tenantID = tID
		t.Logf("[Setup] Extracted tenant_id='%s' from workspace.initiated event on RabbitMQ", tenantID)
	case <-time.After(10 * time.Second):
		t.Fatalf("[Setup] Timed out waiting for workspace.initiated event on RabbitMQ")
	}

	// 5. Poll tenant_manager_db using waitForTenantActive until status is 'active'
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("[Setup] Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	waitForTenantActive(t, db, tenantID)

	// 6. Resolve user_id using resolveUserID helper
	userID = resolveUserID(t, regResp, ownerEmail)

	// 7. Return tenantID, userID, ownerEmail, and a generated password
	password = fmt.Sprintf("Pass_%d!", time.Now().UnixNano()%100000)
	t.Logf("[Setup] Tenant '%s' activated (userID='%s', ownerEmail='%s')", tenantID, userID, ownerEmail)

	return tenantID, userID, ownerEmail, password
}

