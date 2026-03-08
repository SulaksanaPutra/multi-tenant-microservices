/*
 * Test Specification: TC-E2E-012 - Stateful Token Refresh Lifecycle
 * Architectural Scope: auth-service (RefreshHandler, public.refresh_tokens), order-service (JWT Middleware)
 * Objective: Validate the hybrid authentication model by simulating an expired short-lived JWT, confirming downstream
 *            rejection, and successfully rotating credentials via the stateful refresh token endpoint.
 * Failure Mode Guarded: Access token expiration vulnerability, acceptance of expired credentials by downstream services.
 *
 * Workflow / How It Works:
 *   1. Register shared tenant via Gateway POST /api/register and await activation in tenant_manager_db.
 *   2. Provision credentials and login to obtain initial access_token and stateful refresh_token.
 *   3. Generate Synthetic Expired Token: Sign custom JWT claims using valid RSA private key with exp claim 5 minutes in the past.
 *   4. Issue GET /api/orders with expired token and assert downstream HTTP 401 Unauthorized rejection.
 *   5. Token Rotation: Issue POST /auth/refresh with refresh_token and extract new access_token.
 *   6. Re-request Resource: Issue GET /api/orders with new access_token and assert HTTP 200 OK acceptance.
 */

package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestE2E_TC_E2E_012_StatefulTokenRefreshLifecycle(t *testing.T) {
	t.Log("=== TC-E2E-012: Stateful Token Refresh Lifecycle ===")

	// =========================================================================
	// Step 1: Provision Shared Tenant & Await Activation
	// Instruction: Register tenant via Gateway POST /api/register and poll tenant_manager_db
	//              until status transitions to 'active'.
	// =========================================================================
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t)
	t.Logf("2. Tenant '%s' is active.", tenantID)

	// =========================================================================
	// Step 2: Authenticate User & Obtain Session Tokens
	// Instruction: Provision user password credentials and execute login to obtain refresh_token.
	// =========================================================================
	setCredentials(t, userID, tenantID, ownerEmail, password)
	_, refreshToken := loginAndGetTokenPair(t, ownerEmail, password)
	t.Log("3. Credentials provisioned and login successful.")

	// =========================================================================
	// Step 3: Forge Synthetic Expired RS256 JWT
	// Instruction: Load real RSA private key using loadRSAPrivateKey and sign a JWT payload
	//              with ExpiresAt set to 5 minutes ago.
	// Architectural Invariant: Tests whether downstream JWT middleware strictly verifies token expiration claims.
	// =========================================================================
	privateKey := loadRSAPrivateKey(t)
	now := time.Now().UTC()
	expiredClaims := customJWTClaims{
		TenantID: tenantID,
		Email:    ownerEmail,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now.Add(-20 * time.Minute)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-5 * time.Minute)),
			ID:        "synthetic-expired-jti",
		},
	}
	expiredTokenStr, err := jwt.NewWithClaims(jwt.SigningMethodRS256, expiredClaims).SignedString(privateKey)
	if err != nil {
		t.Fatalf("Failed to sign synthetic expired JWT: %v", err)
	}
	t.Log("4. Synthetic expired JWT created.")

	// =========================================================================
	// Step 4: Verify Downstream Rejection of Expired JWT
	// Instruction: Issue GET /api/orders using expired JWT bearer header and assert HTTP 401 Unauthorized.
	// =========================================================================
	reqExpired, _ := http.NewRequest(http.MethodGet, gatewayOrdersURL, nil)
	reqExpired.Header.Set("Authorization", bearerHeader(expiredTokenStr))

	respExpired, err := defaultHTTPClient.Do(reqExpired)
	if err != nil {
		t.Fatalf("GET /api/orders with expired token failed: %v", err)
	}
	defer respExpired.Body.Close()

	if respExpired.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP 401 Unauthorized for expired JWT, got %d", respExpired.StatusCode)
	}
	t.Log("5. Expired JWT correctly rejected with HTTP 401 Unauthorized.")

	// =========================================================================
	// Step 5: Rotate Access Token via Refresh Endpoint
	// Instruction: Issue POST /auth/refresh with refresh_token and extract new access_token.
	// Architectural Invariant: Auth service validates opaque refresh token against public.refresh_tokens.
	// =========================================================================
	refreshBody, _ := json.Marshal(map[string]string{"refresh_token": refreshToken})
	respRefresh, err := defaultHTTPClient.Post(authRefreshURL, "application/json", bytes.NewBuffer(refreshBody))
	if err != nil {
		t.Fatalf("POST /auth/refresh failed: %v", err)
	}
	defer respRefresh.Body.Close()

	if respRefresh.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK for token refresh, got %d", respRefresh.StatusCode)
	}

	var refreshResp authTokenResponse
	if err := json.NewDecoder(respRefresh.Body).Decode(&refreshResp); err != nil {
		t.Fatalf("Failed to decode refresh token response: %v", err)
	}
	newAccessToken := refreshResp.Data.AccessToken
	if newAccessToken == "" {
		t.Fatalf("Refresh response missing access_token")
	}
	t.Log("6. Access token successfully refreshed via POST /auth/refresh.")

	// =========================================================================
	// Step 6: Verify Acceptance of Renewed Access Token
	// Instruction: Issue GET /api/orders with the new access_token and assert HTTP 200 OK.
	// =========================================================================
	reqNew, _ := http.NewRequest(http.MethodGet, gatewayOrdersURL, nil)
	reqNew.Header.Set("Authorization", bearerHeader(newAccessToken))

	respNew, err := defaultHTTPClient.Do(reqNew)
	if err != nil {
		t.Fatalf("GET /api/orders with refreshed token failed: %v", err)
	}
	defer respNew.Body.Close()

	if respNew.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK with refreshed access token, got %d", respNew.StatusCode)
	}
	t.Log("7. Success: Refreshed access token accepted by order-service (HTTP 200 OK).")
}
