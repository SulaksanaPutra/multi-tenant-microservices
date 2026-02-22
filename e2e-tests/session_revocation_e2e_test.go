/*
 * Test Specification: TC-E2E-013 - Immediate Session Revocation (Logout)
 * Architectural Scope: auth-service (LogoutHandler, public.refresh_tokens)
 * Objective: Validate stateful session revocation capability, ensuring that once a refresh token is explicitly
 *            revoked via logout, subsequent credential refresh requests are permanently rejected.
 * Failure Mode Guarded: Persistence of stolen refresh tokens, unauthorized post-logout access token issuance.
 *
 * Workflow / How It Works:
 *   1. Register shared tenant via Gateway POST /api/register and await activation in tenant_manager_db.
 *   2. Provision credentials and login via POST /auth/login to obtain both access_token and refresh_token.
 *   3. Revoke Session: Issue POST /auth/logout with Authorization: Bearer <accessToken> header and refresh_token body.
 *   4. Assert HTTP 200 OK logout response (auth-service updates public.refresh_tokens status to revoked).
 *   5. Replay Token Refresh: Issue POST /auth/refresh with the revoked refresh_token.
 *   6. Assert HTTP 401 Unauthorized response rejection.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	_ "github.com/lib/pq"
)

func TestE2E_TC_E2E_013_ImmediateSessionRevocation(t *testing.T) {
	t.Log("=== TC-E2E-013: Immediate Session Revocation (Logout) ===")

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
	// Step 2: Authenticate User & Obtain Access + Refresh Token Pair
	// Instruction: Call setCredentials and loginAndGetTokenPair to retrieve active session credentials.
	// =========================================================================
	const password = "SecurePassword123!"
	setCredentials(t, userID, tenantID, ownerEmail, password)
	accessToken, refreshToken := loginAndGetTokenPair(t, ownerEmail, password)
	t.Log("3. Credentials provisioned and login successful.")

	// =========================================================================
	// Step 3: Revoke Session via Logout Endpoint
	// Instruction: Issue POST /auth/logout containing Bearer access token and refresh token payload.
	// Architectural Invariant: Auth service marks refresh_tokens database row as revoked.
	// =========================================================================
	logoutBody, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	logoutReq, err := http.NewRequest(http.MethodPost, authServiceURL+"/auth/logout", bytes.NewBuffer(logoutBody))
	if err != nil {
		t.Fatalf("Failed to create POST /auth/logout request: %v", err)
	}
	logoutReq.Header.Set("Content-Type", "application/json")
	logoutReq.Header.Set("Authorization", bearerHeader(accessToken))

	respLogout, err := defaultHTTPClient.Do(logoutReq)
	if err != nil {
		t.Fatalf("POST /auth/logout failed: %v", err)
	}
	defer respLogout.Body.Close()

	if respLogout.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK for logout, got %d", respLogout.StatusCode)
	}
	t.Log("4. Logout successful (HTTP 200 OK).")

	// =========================================================================
	// Step 4: Verify Revoked Refresh Token Rejection
	// Instruction: Submit POST /auth/refresh with the revoked refresh token and assert HTTP 401 Unauthorized.
	// =========================================================================
	refreshBody, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	respRefresh, err := defaultHTTPClient.Post(authRefreshURL, "application/json", bytes.NewBuffer(refreshBody))
	if err != nil {
		t.Fatalf("POST /auth/refresh failed: %v", err)
	}
	defer respRefresh.Body.Close()

	if respRefresh.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP 401 Unauthorized for revoked refresh token, got %d", respRefresh.StatusCode)
	}
	t.Log("5. Success: Refresh with revoked token correctly rejected with HTTP 401 Unauthorized.")
}
