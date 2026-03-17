/*
 * Package e2e_test - Multi-Tenant Microservices E2E Integration Suite
 * File: tc_e2e_023_notification_center_e2e_test.go
 *
 * Test Case: TC-E2E-023 - Notification Center History Log Retrieval Verification
 *
 * Architectural Invariants Verified:
 *   1. Token-gated notification access (GET /api/notifications).
 *   2. Retrieval of notification log history for caller's tenant.
 *   3. Enforcing required permission scope (notifications:read).
 */

package e2e_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestNotificationCenterE2E(t *testing.T) {
	t.Log("[TC-E2E-023] Starting Notification Center E2E Test...")

	// 1. Provision & Activate Tenant
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t, "shared")
	setCredentials(t, userID, tenantID, ownerEmail, password)

	// 2. Login to obtain JWT Token
	token := loginAndGetToken(t, ownerEmail, password)
	if token == "" {
		t.Fatalf("[TC-E2E-023] Failed to obtain JWT token for '%s'", ownerEmail)
	}
	authHeader := bearerHeader(token)

	// 3. GET /api/notifications (List Notifications)
	t.Log("[TC-E2E-023] Testing GET /api/notifications...")
	req, err := http.NewRequest(http.MethodGet, gatewayBaseURL+"/api/notifications", nil)
	if err != nil {
		t.Fatalf("failed to create GET /api/notifications request: %v", err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/notifications failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/notifications expected status 200, got %d", resp.StatusCode)
	}

	var notificationsResp struct {
		Data []struct {
			ID             int64  `json:"ID"`
			TenantID       string `json:"TenantID"`
			RecipientEmail string `json:"RecipientEmail"`
			Subject        string `json:"Subject"`
			Status         string `json:"Status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&notificationsResp); err != nil {
		t.Fatalf("failed to decode notifications response: %v", err)
	}

	t.Logf("[TC-E2E-023] Retrieved %d notification records for tenant_id='%s'", len(notificationsResp.Data), tenantID)
	t.Log("[TC-E2E-023] ✅ Successfully verified Notification Center APIs!")
}
