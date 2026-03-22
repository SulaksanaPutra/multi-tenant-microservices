/*
 * Package e2e_test - Multi-Tenant Microservices E2E Integration Suite
 * File: tc_e2e_020_user_profile_management_e2e_test.go
 *
 * Test Case: TC-E2E-020 - User Profile Management & User Listing Verification
 *
 * Architectural Invariants Verified:
 *   1. Token-gated access to user-service endpoints via Traefik Gateway.
 *   2. Retrieval of tenant users list (GET /api/users).
 *   3. Authenticated user profile modification (PUT /api/users/me).
 */

package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestUserProfileManagementE2E(t *testing.T) {
	t.Log("[TC-E2E-020] Starting User Profile Management E2E Test...")

	// 1. Provision & Activate Tenant
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t, "shared")
	setCredentials(t, userID, tenantID, ownerEmail, password)

	// 2. Login to obtain JWT Token
	token := loginAndGetToken(t, ownerEmail, password)
	if token == "" {
		t.Fatalf("[TC-E2E-020] Failed to obtain JWT token for '%s'", ownerEmail)
	}
	authHeader := bearerHeader(token)

	// 3. GET /api/users (List Users in Tenant)
	t.Log("[TC-E2E-020] Testing GET /api/users...")
	req, err := http.NewRequest(http.MethodGet, gatewayBaseURL+"/api/users", nil)
	if err != nil {
		t.Fatalf("failed to create GET /api/users request: %v", err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/users failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/users expected status 200, got %d", resp.StatusCode)
	}

	var listUsersResp struct {
		Data []struct {
			ID    string `json:"ID"`
			Email string `json:"Email"`
			Name  string `json:"Name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listUsersResp); err != nil {
		t.Fatalf("failed to decode list users response: %v", err)
	}
	if len(listUsersResp.Data) == 0 {
		t.Error("expected non-empty users list")
	}

	// 4. PUT /api/users/me (Update User Profile Name)
	t.Log("[TC-E2E-020] Testing PUT /api/users/me...")
	updateMeBody, _ := json.Marshal(map[string]string{
		"name": "Owner Updated Profile Name",
	})
	req, err = http.NewRequest(http.MethodPut, gatewayBaseURL+"/api/users/me", bytes.NewBuffer(updateMeBody))
	if err != nil {
		t.Fatalf("failed to create PUT /api/users/me request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	resp, err = defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/users/me failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/users/me expected status 200, got %d", resp.StatusCode)
	}

	t.Log("[TC-E2E-020] Successfully verified User Profile Management APIs!")
}
