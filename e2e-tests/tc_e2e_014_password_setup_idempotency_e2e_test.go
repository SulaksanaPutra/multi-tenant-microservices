/*
 * Test Specification: TC-E2E-014 - Password Setup Token Single-Use Idempotency
 * Architectural Scope: auth-service (public.password_setup_tokens, CredentialsSetupHandler)
 * Objective: Prevent account hijacking race conditions and token reuse attacks by validating that
 *            a password setup token can only be consumed exactly once.
 * Failure Mode Guarded: Replay attacks on setup tokens, account takeover of newly provisioned users.
 *
 * Workflow / How It Works:
 *   1. Register a shared tenant via Gateway POST /api/register and await activation in tenant_manager_db.
 *   2. Request setup token directly via auth-service internal endpoint POST /internal/auth/setup-token.
 *   3. First Consumption: Submit token and new password to POST /auth/credentials/setup — assert HTTP 200 OK.
 *   4. Replay Attempt: Resubmit the exact same token payload to POST /auth/credentials/setup.
 *   5. Assert HTTP 400 Bad Request / 409 Conflict with token already consumed error envelope.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	_ "github.com/lib/pq"
)

func TestE2E_TC_E2E_014_PasswordSetupTokenSingleUseIdempotency(t *testing.T) {
	t.Log("=== TC-E2E-014: Password Setup Token Single-Use Idempotency ===")

	// =========================================================================
	// Step 1: Provision Shared Tenant & Await Activation
	// Instruction: Register tenant via Gateway POST /api/register and poll tenant_manager_db
	//              until status transitions to 'active'.
	// =========================================================================
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	t.Logf("1. Registering tenant: owner='%s', email='%s', plan='shared'", ownerName, ownerEmail)

	regBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(regBody))
	if err != nil {
		t.Fatalf("POST /api/register failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP 202 Accepted, got %d", resp.StatusCode)
	}

	var regResp RegisterResp
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("Failed to decode register response: %v", err)
	}
	tenantID := regResp.Data.TenantID

	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	waitForTenantActive(t, db, tenantID)
	t.Logf("2. Tenant '%s' is active.", tenantID)

	userID := resolveUserID(t, regResp, ownerEmail)

	// =========================================================================
	// Step 2: Retrieve Raw Setup Token via Internal Endpoint
	// Instruction: Issue HTTP POST to auth-service internal endpoint /internal/auth/setup-token
	//              with X-Internal-Service-Token authorization header.
	// =========================================================================
	tokenReqBody, _ := json.Marshal(map[string]string{
		"user_id":   userID,
		"tenant_id": tenantID,
		"email":     ownerEmail,
	})

	tokenReq, err := http.NewRequest(http.MethodPost, authServiceURL+"/internal/auth/setup-token", bytes.NewBuffer(tokenReqBody))
	if err != nil {
		t.Fatalf("Failed to create internal setup-token request: %v", err)
	}
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenReq.Header.Set("X-Internal-Service-Token", "default_internal_service_token")

	respToken, err := defaultHTTPClient.Do(tokenReq)
	if err != nil {
		t.Fatalf("POST /internal/auth/setup-token failed: %v", err)
	}
	defer respToken.Body.Close()

	if respToken.StatusCode != http.StatusOK {
		t.Fatalf("Internal setup-token returned status %d", respToken.StatusCode)
	}

	var tokenResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(respToken.Body).Decode(&tokenResp); err != nil || tokenResp.Data.Token == "" {
		t.Fatalf("Failed to parse setup token from response: %v", err)
	}
	rawSetupToken := tokenResp.Data.Token
	t.Log("3. Raw setup token obtained from internal endpoint.")

	// =========================================================================
	// Step 3: First Password Setup Consumption
	// Instruction: Issue POST /auth/credentials/setup with raw token and new password.
	// Architectural Invariant: Token is validated, used_at timestamp is populated in DB,
	//                          and HTTP 200 OK is returned.
	// =========================================================================
	const password = "SecurePassword123!"
	setupPayload, _ := json.Marshal(map[string]string{
		"token":    rawSetupToken,
		"password": password,
	})

	respSetup1, err := defaultHTTPClient.Post(authServiceURL+"/auth/credentials/setup", "application/json", bytes.NewBuffer(setupPayload))
	if err != nil {
		t.Fatalf("First POST /auth/credentials/setup failed: %v", err)
	}
	defer respSetup1.Body.Close()

	if respSetup1.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK for first password setup, got %d", respSetup1.StatusCode)
	}
	t.Log("4. First password setup consumed the token successfully (HTTP 200 OK).")

	// =========================================================================
	// Step 4: Replay Attempt (Double-Spend Protection)
	// Instruction: Resubmit the exact same token payload to POST /auth/credentials/setup.
	// Architectural Invariant: Auth service detects non-null used_at status and rejects attempt
	//                          with HTTP 400 Bad Request or HTTP 409 Conflict.
	// =========================================================================
	respSetup2, err := defaultHTTPClient.Post(authServiceURL+"/auth/credentials/setup", "application/json", bytes.NewBuffer(setupPayload))
	if err != nil {
		t.Fatalf("Second POST /auth/credentials/setup failed: %v", err)
	}
	defer respSetup2.Body.Close()

	if respSetup2.StatusCode != http.StatusBadRequest && respSetup2.StatusCode != http.StatusConflict {
		t.Fatalf("Expected HTTP 400 Bad Request for double-spend token attempt, got %d", respSetup2.StatusCode)
	}

	body, _ := io.ReadAll(respSetup2.Body)
	t.Logf("5. Success: Double-spend attempt correctly rejected with status %d: %s", respSetup2.StatusCode, string(body))
}
