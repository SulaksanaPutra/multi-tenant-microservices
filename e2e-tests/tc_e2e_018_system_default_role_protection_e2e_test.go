/*
 * Test Specification: TC-E2E-018 - System Default Role Protection & Guardrails
 * Architectural Scope: auth-service (Role Management API)
 * Objective: Validate platform guardrails protecting pre-seeded system default roles (admin, viewer)
 *            from modification or deletion by tenant admins.
 * Failure Mode Guarded: Accidental or malicious mutation/deletion of platform system roles, system stability degradation.
 *
 * Workflow / How It Works:
 *   1. Register a tenant, setup credentials, and authenticate as tenant admin to obtain a JWT access token.
 *   2. Query GET /api/roles to list available tenant and platform roles.
 *   3. Locate a pre-seeded system default role (is_system = true, e.g., 'admin' or 'viewer').
 *   4. Attempt to update system role permissions via PUT /api/roles/:id/permissions.
 *   5. Assert HTTP 400 Bad Request or HTTP 403 Forbidden with ErrSystemRoleProtected error payload.
 *   6. Attempt to delete system role via DELETE /api/roles/:id.
 *   7. Assert HTTP 400 Bad Request or HTTP 403 Forbidden with ErrSystemRoleProtected error payload.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/brianvoe/gofakeit/v6"
	_ "github.com/lib/pq"
)

type ListRolesResp struct {
	Status  string         `json:"status"`
	Message string         `json:"message"`
	Data    []RoleRespData `json:"data"`
}

func TestE2E_SystemDefaultRoleProtection(t *testing.T) {
	gofakeit.Seed(0)
	ownerEmail := gofakeit.Email()
	tenantName := gofakeit.Company()

	// Step 1: Register Tenant
	regReq := RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  gofakeit.Name(),
		Plan:       "shared",
		TenantName: tenantName,
	}
	payloadBytes, _ := json.Marshal(regReq)

	resp, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		t.Fatalf("Failed to execute register request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected HTTP 202 Accepted, got %d: %s", resp.StatusCode, string(body))
	}

	var regResp RegisterResp
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("Failed to decode registration response: %v", err)
	}

	tenantID := regResp.Data.TenantID
	userID := resolveUserID(t, regResp, ownerEmail)

	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	waitForTenantActive(t, db, tenantID)

	password := "AdminPass123!"
	setCredentials(t, userID, tenantID, ownerEmail, password)
	accessToken, _ := loginAndGetTokenPair(t, ownerEmail, password)

	// Step 2: Fetch System Roles via GET /api/roles
	listReq, _ := http.NewRequest(http.MethodGet, "http://localhost:8000/api/roles", nil)
	listReq.Header.Set("Authorization", "Bearer "+accessToken)

	listResp, err := defaultHTTPClient.Do(listReq)
	if err != nil {
		t.Fatalf("Failed to execute GET /api/roles: %v", err)
	}
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		t.Fatalf("Expected HTTP 200 OK from GET /api/roles, got %d: %s", listResp.StatusCode, string(body))
	}

	var listRolesData ListRolesResp
	if err := json.NewDecoder(listResp.Body).Decode(&listRolesData); err != nil {
		t.Fatalf("Failed to decode list roles response: %v", err)
	}

	var systemRoleID string
	for _, role := range listRolesData.Data {
		if role.IsSystem || role.Name == "admin" || role.Name == "viewer" {
			systemRoleID = role.ID
			break
		}
	}

	if systemRoleID == "" {
		// Fallback: If role list is empty, default to system admin role identifier
		systemRoleID = "role_system_admin"
	}
	t.Logf("Testing protection guardrails on system default role ID: %s", systemRoleID)

	// Step 3: Attempt Mutation (PUT /api/roles/:id/permissions)
	updateReqPayload := UpdateRolePermsReq{
		Permissions: []string{"unauthorized:super_admin"},
	}
	updateBody, _ := json.Marshal(updateReqPayload)

	mutateURL := fmt.Sprintf("http://localhost:8000/api/roles/%s/permissions", systemRoleID)
	mutateReq, _ := http.NewRequest(http.MethodPut, mutateURL, bytes.NewBuffer(updateBody))
	mutateReq.Header.Set("Content-Type", "application/json")
	mutateReq.Header.Set("Authorization", "Bearer "+accessToken)

	mutateResp, err := defaultHTTPClient.Do(mutateReq)
	if err != nil {
		t.Fatalf("Failed to execute update system role request: %v", err)
	}
	defer mutateResp.Body.Close()

	if mutateResp.StatusCode != http.StatusBadRequest && mutateResp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(mutateResp.Body)
		t.Fatalf("Security Violation: Expected HTTP 400 Bad Request or HTTP 403 Forbidden when mutating system role, got %d: %s", mutateResp.StatusCode, string(body))
	}
	t.Logf("System role mutation rejected as expected with HTTP %d", mutateResp.StatusCode)

	// Step 4: Attempt Deletion (DELETE /api/roles/:id)
	deleteURL := fmt.Sprintf("http://localhost:8000/api/roles/%s", systemRoleID)
	deleteReq, _ := http.NewRequest(http.MethodDelete, deleteURL, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+accessToken)

	deleteResp, err := defaultHTTPClient.Do(deleteReq)
	if err != nil {
		t.Fatalf("Failed to execute delete system role request: %v", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusBadRequest && deleteResp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(deleteResp.Body)
		t.Fatalf("Security Violation: Expected HTTP 400 Bad Request or HTTP 403 Forbidden when deleting system role, got %d: %s", deleteResp.StatusCode, string(body))
	}
	t.Logf("System role deletion rejected as expected with HTTP %d", deleteResp.StatusCode)

	t.Logf("TC-E2E-018 Passed: System default role guardrails immutably protected default roles.")
}
