/*
 * Package e2e_test - Multi-Tenant Microservices E2E Integration Suite
 * File: tc_e2e_022_user_role_assignment_and_permissions_e2e_test.go
 *
 * Test Case: TC-E2E-022 - System Permissions Catalog Fetch, Custom RBAC Role Creation & User Role Assignment
 *
* Architectural Invariants Verified:
 *   1. Token-gated permissions catalog retrieval (GET /api/auth/permissions).
 *   2. Tenant-scoped custom role creation using system permissions (POST /api/auth/roles).
 *   3. Available roles listing for tenant (GET /api/auth/roles).
 *   4. User role assignment (PUT /api/auth/users/:userID/role) and user role details lookup (GET /api/auth/users/:userID/role).
*/

package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"auth-service/internal/handler"
	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
)

func TestUserRoleAssignmentAndPermissionsE2E(t *testing.T) {
	t.Log("[TC-E2E-022] Starting User Role Assignment & Permissions E2E Test...")

	// 1. Provision & Activate Tenant
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t, "shared")
	setCredentials(t, userID, tenantID, ownerEmail, password)

	// 2. Login to obtain JWT Token
	token := loginAndGetToken(t, ownerEmail, password)
	if token == "" {
		t.Fatalf("[TC-E2E-022] Failed to obtain JWT token for '%s'", ownerEmail)
	}
	authHeader := bearerHeader(token)

	// 3. GET /api/auth/permissions (List System Permissions Catalog)
	t.Log("[TC-E2E-022] Testing GET /api/auth/permissions...")
	req, err := http.NewRequest(http.MethodGet, gatewayBaseURL+"/api/auth/permissions", nil)
	if err != nil {
		t.Fatalf("failed to create GET /api/auth/permissions request: %v", err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/auth/permissions failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/auth/permissions expected status 200, got %d", resp.StatusCode)
	}

	// 4. POST /api/auth/roles (Create Tenant Custom Role)
	t.Log("[TC-E2E-022] Testing POST /api/auth/roles...")
	createRoleBody, _ := json.Marshal(map[string]any{
		"name":        "e2e_editor",
		"description": "Custom Editor Role for E2E Test",
		"permissions": []string{"users:read", "orders:read"},
	})
	req, err = http.NewRequest(http.MethodPost, gatewayBaseURL+"/api/auth/roles", bytes.NewBuffer(createRoleBody))
	if err != nil {
		t.Fatalf("failed to create POST /api/auth/roles request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	resp, err = defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/auth/roles failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/auth/roles expected 201/200, got %d: %s", resp.StatusCode, string(respBody))
	}

	var createdRole httputil.StandardResponse[handler.RoleResponse]
	if err := json.NewDecoder(resp.Body).Decode(&createdRole); err != nil {
		t.Fatalf("failed to decode created role response: %v", err)
	}
	if createdRole.Data.ID == "" {
		t.Fatalf("expected non-empty role_id in response")
	}

	// 5. GET /api/auth/roles (List Tenant Roles)
	t.Log("[TC-E2E-022] Testing GET /api/auth/roles...")
	req, err = http.NewRequest(http.MethodGet, gatewayBaseURL+"/api/auth/roles", nil)
	if err != nil {
		t.Fatalf("failed to create GET /api/auth/roles request: %v", err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err = defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/auth/roles failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/auth/roles expected status 200, got %d", resp.StatusCode)
	}

	// 6. PUT /api/auth/users/:userID/role (Assign Role to User)
	t.Log("[TC-E2E-022] Testing PUT /api/auth/users/:userID/role...")
	assignRoleBody, _ := json.Marshal(map[string]string{
		"role_id": createdRole.Data.ID,
	})
	req, err = http.NewRequest(http.MethodPut, gatewayBaseURL+"/api/auth/users/"+userID+"/role", bytes.NewBuffer(assignRoleBody))
	if err != nil {
		t.Fatalf("failed to create PUT /api/auth/users/:userID/role request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	resp, err = defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/auth/users/:userID/role failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/auth/users/:userID/role expected status 200, got %d", resp.StatusCode)
	}

	// 7. GET /api/auth/users/:userID/role (Get User Role Assignment)
	t.Log("[TC-E2E-022] Testing GET /api/auth/users/:userID/role...")
	req, err = http.NewRequest(http.MethodGet, gatewayBaseURL+"/api/auth/users/"+userID+"/role", nil)
	if err != nil {
		t.Fatalf("failed to create GET /api/auth/users/:userID/role request: %v", err)
	}
	req.Header.Set("Authorization", authHeader)
	resp, err = defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/auth/users/:userID/role failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/auth/users/:userID/role expected status 200, got %d", resp.StatusCode)
	}

	t.Log("[TC-E2E-022] Successfully verified User Role Assignment & Permissions APIs!")
}
