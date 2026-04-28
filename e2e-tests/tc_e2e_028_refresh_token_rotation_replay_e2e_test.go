/*
 * Test Specification: TC-E2E-028 - Stateful Token Rotation, Replay Rejection & Workspace Exchange Security
 * Architectural Scope: auth-service (RefreshHandler, SelectWorkspaceHandler, public.refresh_tokens,
 *                      public.password_setup_tokens), unified-identity workspace selection.
 * Objective: Validate the token lifecycle abuse plane:
 *            A. Refresh-token rotation: after a successful refresh, the rotated (old) refresh token is deleted
 *               and any replay attempt is rejected with HTTP 401 Unauthorized.
 *            B. Workspace-selection exchange tokens are single-use: replaying an already-consumed exchange token
 *               is rejected (400), selecting a non-member tenant is rejected (400), and forged exchange tokens
 *               are rejected (400).
 * Failure Mode Guarded: Session hijacking via stolen refresh-token replay, indefinite token reuse,
 *                       cross-tenant escalation through workspace selection exchange tokens.
 *
 * Workflow / How It Works (A):
 *   1. Register tenant, provision credentials, login to obtain refresh_token_1.
 *   2. Refresh with refresh_token_1 -> HTTP 200, returns refresh_token_2 (rotation).
 *   3. Replay refresh_token_1 -> HTTP 401 (rotated token deleted from public.refresh_tokens).
 *   4. Refresh with refresh_token_2 -> HTTP 200, returns refresh_token_3.
 *   5. Replay refresh_token_2 -> HTTP 401.
 *
 * Workflow / How It Works (B):
 *   1. Register two independent tenants under the SAME owner email (unified identity).
 *   2. Login -> SELECT_WORKSPACE response with exchange_token + workspaces.
 *   3. Select tenant A -> HTTP 200 (exchange token consumed).
 *   4. Replay the same exchange_token for tenant B -> HTTP 400 (ErrTokenAlreadyUsed).
 *   5. Login again -> fresh exchange_token; select a non-member tenant -> HTTP 400 (ErrTenantMembershipNotFound).
 *   6. Select-tenant with a forged exchange token -> HTTP 400 (ErrTokenNotFound).
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestE2E_TC_E2E_028_RefreshTokenRotation_ReplayRejection(t *testing.T) {
	t.Log("=== TC-E2E-028 (A): Refresh-Token Rotation & Replay Rejection ===")

	// =========================================================================
	// Step 1: Provision Tenant & Login
	// =========================================================================
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t)
	setCredentials(t, userID, tenantID, ownerEmail, password)

	_, refreshToken1 := loginAndGetTokenPair(t, ownerEmail, password)
	if refreshToken1 == "" {
		t.Fatalf("Failed to obtain initial refresh token")
	}
	t.Log("2. Acquired initial refresh_token_1.")

	// =========================================================================
	// Step 2: Rotate refresh_token_1 -> refresh_token_2
	// =========================================================================
	status, _, refreshToken2 := doRefreshToken(t, refreshToken1)
	if status != http.StatusOK {
		t.Fatalf("Expected HTTP 200 on first refresh (rotation), got %d", status)
	}
	if refreshToken2 == "" || refreshToken2 == refreshToken1 {
		t.Fatalf("Refresh rotation failed: expected a NEW refresh token")
	}
	t.Log("3. Successfully rotated to refresh_token_2.")

	// =========================================================================
	// Step 3: Replay old refresh_token_1 -> must be rejected
	// Architectural Invariant: RefreshToken deletes the consumed row; replay hits ErrTokenNotFound -> 401.
	// =========================================================================
	status, _, _ = doRefreshToken(t, refreshToken1)
	if status != http.StatusUnauthorized {
		t.Fatalf("Security Violation: Replay of rotated refresh_token_1 expected HTTP 401, got %d", status)
	}
	t.Log("4. Replay of rotated refresh_token_1 correctly rejected (HTTP 401).")

	// =========================================================================
	// Step 4: Rotate refresh_token_2 -> refresh_token_3, then replay refresh_token_2
	// =========================================================================
	status, _, refreshToken3 := doRefreshToken(t, refreshToken2)
	if status != http.StatusOK || refreshToken3 == "" {
		t.Fatalf("Expected HTTP 200 on second refresh (rotation), got %d", status)
	}

	status, _, _ = doRefreshToken(t, refreshToken2)
	if status != http.StatusUnauthorized {
		t.Fatalf("Security Violation: Replay of rotated refresh_token_2 expected HTTP 401, got %d", status)
	}
	t.Logf("5. TC-E2E-028 (A) Passed: rotated refresh tokens permanently rejected (chain rt1->rt2->rt3).")
}

func TestE2E_TC_E2E_028_SelectTenantExchangeTokenReplay(t *testing.T) {
	t.Log("=== TC-E2E-028 (B): Workspace Selection Exchange-Token Single-Use Enforcement ===")

	// =========================================================================
	// Step 1: Register Tenant A (random email)
	// =========================================================================
	tenantIDA, userIDA, emailA, passwordA := registerAndActivateTenant(t)
	setCredentials(t, userIDA, tenantIDA, emailA, passwordA)
	t.Logf("2. Registered Tenant A id='%s'.", tenantIDA)

	// =========================================================================
	// Step 2: Register Tenant B under the SAME owner email (unified identity)
	// =========================================================================
	ownerName, _, tenantNameB, _ := generateFakeData("shared")
	reqBodyB, _ := json.Marshal(RegisterReq{
		OwnerEmail: emailA,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantNameB + " Beta",
	})

	respB, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBodyB))
	if err != nil {
		t.Fatalf("Failed to register Tenant B: %v", err)
	}
	defer respB.Body.Close()
	if respB.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP 202 for Tenant B registration, got %d", respB.StatusCode)
	}

	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	var tenantIDB string
	err = db.QueryRow(
		"SELECT id FROM public.tenants WHERE owner_email = $1 ORDER BY created_at DESC LIMIT 1",
		emailA,
	).Scan(&tenantIDB)
	if err != nil || tenantIDB == "" {
		t.Fatalf("Failed to resolve Tenant B id: %v", err)
	}
	waitForTenantActive(t, db, tenantIDB)
	setCredentials(t, userIDA, tenantIDB, emailA, passwordA)
	t.Logf("3. Registered & activated Tenant B id='%s' under the same email.", tenantIDB)

	// =========================================================================
	// Step 3: Login -> SELECT_WORKSPACE (two memberships) with exchange_token
	// =========================================================================
	exchangeToken, workspaces := loginForWorkspaceSelection(t, emailA, passwordA)
	if exchangeToken == "" || len(workspaces) != 2 {
		t.Fatalf("Expected SELECT_WORKSPACE with 2 workspaces, got exchange_token='%s', workspaces=%v", exchangeToken, workspaces)
	}
	t.Logf("4. Login returned SELECT_WORKSPACE with exchange_token and %d workspaces.", len(workspaces))

	// =========================================================================
	// Step 4: Consume exchange token for Tenant A -> 200
	// =========================================================================
	status := selectTenant(t, exchangeToken, tenantIDA)
	if status != http.StatusOK {
		t.Fatalf("Expected HTTP 200 selecting Tenant A, got %d", status)
	}
	t.Log("5. Exchange token consumed for Tenant A (HTTP 200).")

	// =========================================================================
	// Step 5: Replay the SAME exchange token for Tenant B -> 400 (already used)
	// Architectural Invariant: MarkTokenUsed sets used_at atomically; replay yields ErrTokenAlreadyUsed -> 400.
	// =========================================================================
	status = selectTenant(t, exchangeToken, tenantIDB)
	if status != http.StatusBadRequest {
		t.Fatalf("Security Violation: Replay of consumed exchange_token expected HTTP 400, got %d", status)
	}
	t.Log("6. Replay of consumed exchange_token correctly rejected (HTTP 400).")

	// =========================================================================
	// Step 6: Fresh login -> select a non-member tenant -> 400 (membership check)
	// =========================================================================
	exchangeToken2, _ := loginForWorkspaceSelection(t, emailA, passwordA)
	status = selectTenant(t, exchangeToken2, "tnt_forged_nonmember_000")
	if status != http.StatusBadRequest {
		t.Fatalf("Security Violation: Selecting a non-member tenant expected HTTP 400, got %d", status)
	}
	t.Log("7. Non-member tenant selection correctly rejected (HTTP 400).")

	// =========================================================================
	// Step 7: Forged exchange token -> 400 (token not found)
	// =========================================================================
	status = selectTenant(t, "forged_exchange_token_abc123", tenantIDA)
	if status != http.StatusBadRequest {
		t.Fatalf("Security Violation: Forged exchange token expected HTTP 400, got %d", status)
	}
	t.Log("8. Forged exchange token correctly rejected (HTTP 400).")

	t.Logf("9. TC-E2E-028 (B) Passed: workspace exchange tokens are single-use and membership-scoped.")
}

// doRefreshToken issues POST /api/auth/refresh and returns the status code plus the rotated pair.
func doRefreshToken(t *testing.T, refreshToken string) (statusCode int, accessToken, newRefreshToken string) {
	t.Helper()

	body, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	resp, err := defaultHTTPClient.Post(authRefreshURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("POST /api/auth/refresh failed: %v", err)
	}
	defer resp.Body.Close()

	var refreshResp authTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&refreshResp); err != nil {
		t.Fatalf("Failed to decode refresh response: %v", err)
	}
	return resp.StatusCode, refreshResp.Data.AccessToken, refreshResp.Data.RefreshToken
}

// loginForWorkspaceSelection issues POST /api/auth/login and parses the SELECT_WORKSPACE response,
// returning the exchange token and the list of selectable tenant workspaces.
func loginForWorkspaceSelection(t *testing.T, email, password string) (exchangeToken string, workspaces []string) {
	t.Helper()

	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	resp, err := defaultHTTPClient.Post(authLoginURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("POST /api/auth/login failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Login expected HTTP 200, got %d", resp.StatusCode)
	}

	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("Failed to decode login response: %v", err)
	}

	dataMap, _ := raw["data"].(map[string]any)
	if dataMap == nil {
		t.Fatalf("Login response missing data object: %v", raw)
	}

	statusStr, _ := dataMap["status"].(string)
	if statusStr != "SELECT_WORKSPACE" {
		t.Fatalf("Expected SELECT_WORKSPACE status, got '%s'", statusStr)
	}

	exchangeToken, _ = dataMap["exchange_token"].(string)
	wsList, _ := dataMap["workspaces"].([]any)
	for _, ws := range wsList {
		if wsMap, ok := ws.(map[string]any); ok {
			if tid, ok := wsMap["tenant_id"].(string); ok {
				workspaces = append(workspaces, tid)
			}
		}
	}

	// Slight backoff so a subsequent login mints a genuinely new exchange token.
	time.Sleep(200 * time.Millisecond)
	return exchangeToken, workspaces
}

// selectTenant issues POST /api/auth/select-tenant and returns the response status code.
func selectTenant(t *testing.T, exchangeToken, tenantID string) int {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"exchange_token": exchangeToken,
		"tenant_id":      tenantID,
	})
	resp, err := defaultHTTPClient.Post(authServiceURL+"/api/auth/select-tenant", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("POST /api/auth/select-tenant failed: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
