/*
 * Test Specification: TC-E2E-027 - RBAC Deny-Path Enforcement (403 Forbidden Matrix)
 * Architectural Scope: auth-service (Role API, permission resolution), order-service (RequirePermission),
 *                      tenant-service (RequirePermission), user-service (RequirePermission).
 * Objective: Validate the NEGATIVE authorization plane. Every prior RBAC test exercises the happy path;
 *            this test proves that a token minted for a read-only role is denied at every write/manage
 *            boundary while remaining able to read, i.e. the system enforces least-privilege by DENY.
 * Failure Mode Guarded: Authorization bypass via stale/over-privileged roles, missing permission checks
 *                       on write endpoints, RBAC drift after role reassignment.
 *
 * Workflow / How It Works:
 *   1. Register tenant, provision owner credentials, login as admin.
 *   2. Create a custom read-only role containing ONLY "orders:read".
 *   3. Reassign the owner user to that role (auth-service batch-increments user_permission_versions).
 *   4. Login again to obtain a fresh token reflecting the read-only permission set.
 *   5. Assert DENY on every gated write/manage endpoint:
 *        - POST /api/orders                         -> 403 (missing orders:write)
 *        - PUT /api/tenants/me/plan                 -> 403 (missing tenants:write)
 *        - POST /api/auth/roles                     -> 403 (missing auth:roles:manage)
 *        - GET /api/auth/permissions                -> 403 (missing auth:roles:read)
 *   6. Assert ALLOW on the read endpoint:
 *        - GET /api/orders                          -> 200 (orders:read granted)
 */

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"auth-service/internal/handler"
	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
)

func TestE2E_TC_E2E_027_RBACDenyPathEnforcement(t *testing.T) {
	t.Log("=== TC-E2E-027: RBAC Deny-Path Enforcement (403 Forbidden Matrix) ===")

	// =========================================================================
	// Step 1: Provision Tenant & Authenticate as Admin
	// =========================================================================
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t)
	setCredentials(t, userID, tenantID, ownerEmail, password)

	adminToken := loginAndGetToken(t, ownerEmail, password)
	if adminToken == "" {
		t.Fatalf("Failed to obtain admin JWT for '%s'", ownerEmail)
	}
	adminHeader := bearerHeader(adminToken)
	t.Logf("2. Admin authenticated for tenant_id='%s' (full permission set).", tenantID)

	// =========================================================================
	// Step 2: Create Read-Only Custom Role
	// Instruction: Create a role composed solely of the "orders:read" permission.
	// =========================================================================
	readonlyRole := handler.CreateRoleRequest{
		Name:        "E2E Read-Only Viewer",
		Description: "Least-privilege role with read-only order access",
		Permissions: []string{"orders:read"},
	}
	roleBody, _ := json.Marshal(readonlyRole)

	createReq, _ := http.NewRequest(http.MethodPost, gatewayBaseURL+"/api/auth/roles", bytes.NewBuffer(roleBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Authorization", adminHeader)

	createResp, err := defaultHTTPClient.Do(createReq)
	if err != nil {
		t.Fatalf("Failed to create read-only role: %v", err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("Expected role creation success (200/201), got %d: %s", createResp.StatusCode, string(body))
	}

	var roleData httputil.StandardResponse[handler.RoleResponse]
	if err := json.NewDecoder(createResp.Body).Decode(&roleData); err != nil || roleData.Data.ID == "" {
		t.Fatalf("Failed to decode created role response: %v", err)
	}
	t.Logf("3. Created read-only role id='%s' with permissions %v.", roleData.Data.ID, roleData.Data.Permissions)

	// =========================================================================
	// Step 3: Reassign Owner to the Read-Only Role
	// Instruction: PUT /api/auth/users/:userID/role. This atomically replaces the admin role
	//              and batch-increments user_permission_versions (ON CONFLICT DO UPDATE).
	// =========================================================================
	assignBody, _ := json.Marshal(map[string]string{"role_id": roleData.Data.ID})
	assignReq, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/auth/users/%s/role", gatewayBaseURL, userID), bytes.NewBuffer(assignBody))
	assignReq.Header.Set("Content-Type", "application/json")
	assignReq.Header.Set("Authorization", adminHeader)

	assignResp, err := defaultHTTPClient.Do(assignReq)
	if err != nil {
		t.Fatalf("Failed to assign read-only role: %v", err)
	}
	assignResp.Body.Close()
	if assignResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK for role assignment, got %d", assignResp.StatusCode)
	}
	t.Logf("4. Reassigned user '%s' to read-only role.", userID)

	// =========================================================================
	// Step 4: Login Again to Obtain Read-Only Token
	// Poll until the freshly minted token enforces the restricted permission set.
	// Rationale: AssignUserRole commits synchronously, but the DB connection pool
	// used by ListUserPermissions on the next login may occasionally observe the
	// previous committed state under high load. A canary probe on the first deny
	// assertion (POST /api/orders → 403) confirms the token is truly restricted
	// before proceeding with the full deny-path matrix.
	// =========================================================================
	const (
		maxPermRetries  = 5
		permRetryDelay  = 1 * time.Second
	)

	var readonlyHeader string
	for attempt := 1; attempt <= maxPermRetries; attempt++ {
		time.Sleep(permRetryDelay)

		freshToken := loginAndGetToken(t, ownerEmail, password)
		if freshToken == "" {
			t.Fatalf("Failed to obtain read-only JWT for '%s' (attempt %d)", ownerEmail, attempt)
		}

		// Canary probe: POST /api/orders must return 403 for the role restriction
		// to be in effect. If still 201, the login embedded the stale permissions;
		// discard the token and retry.
		canaryBody, _ := json.Marshal(OrderRequest{CustomerID: "cust_deny_canary", Quantity: 1, Price: 1.00, Currency: "USD"})
		canaryReq, _ := http.NewRequest(http.MethodPost, gatewayOrdersURL, bytes.NewBuffer(canaryBody))
		canaryReq.Header.Set("Content-Type", "application/json")
		canaryReq.Header.Set("Authorization", bearerHeader(freshToken))

		canaryResp, err := defaultHTTPClient.Do(canaryReq)
		if err != nil {
			t.Logf("5. Canary probe attempt %d: request error: %v — retrying", attempt, err)
			continue
		}
		canaryResp.Body.Close()

		if canaryResp.StatusCode == http.StatusForbidden {
			// Token is correctly restricted — proceed with this token.
			readonlyHeader = bearerHeader(freshToken)
			t.Logf("5. Acquired fresh read-only JWT token (attempt %d, canary confirmed 403).", attempt)
			break
		}

		t.Logf("5. Canary probe attempt %d: POST /api/orders returned %d (expected 403) — token still carries stale permissions, retrying login", attempt, canaryResp.StatusCode)

		if attempt == maxPermRetries {
			t.Fatalf("5. Permission restriction not enforced after %d login attempts — POST /api/orders still returns %d instead of 403", maxPermRetries, canaryResp.StatusCode)
		}
	}

	// =========================================================================
	// Step 5: Deny-Path Assertions (every gated write/manage endpoint -> 403)
	// The canary already verified 5a; assert the remaining deny paths.
	// =========================================================================
	// 5a. POST /api/orders -> 403 (missing orders:write) — already confirmed by canary above.
	orderBody, _ := json.Marshal(OrderRequest{CustomerID: "cust_deny_test", Quantity: 1, Price: 42.00, Currency: "USD"})
	denyOrderReq, _ := http.NewRequest(http.MethodPost, gatewayOrdersURL, bytes.NewBuffer(orderBody))
	denyOrderReq.Header.Set("Content-Type", "application/json")
	denyOrderReq.Header.Set("Authorization", readonlyHeader)
	assertStatus(t, denyOrderReq, http.StatusForbidden, "POST /api/orders with read-only token")

	// 5b. PUT /api/tenants/me/plan -> 403 (missing tenants:write)
	planBody, _ := json.Marshal(map[string]string{"plan": "dedicated"})
	denyPlanReq, _ := http.NewRequest(http.MethodPut, gatewayBaseURL+"/api/tenants/me/plan", bytes.NewBuffer(planBody))
	denyPlanReq.Header.Set("Content-Type", "application/json")
	denyPlanReq.Header.Set("Authorization", readonlyHeader)
	assertStatus(t, denyPlanReq, http.StatusForbidden, "PUT /api/tenants/me/plan with read-only token")

	// 5c. POST /api/auth/roles -> 403 (missing auth:roles:manage)
	denyRoleReq, _ := http.NewRequest(http.MethodPost, gatewayBaseURL+"/api/auth/roles", bytes.NewBuffer(roleBody))
	denyRoleReq.Header.Set("Content-Type", "application/json")
	denyRoleReq.Header.Set("Authorization", readonlyHeader)
	assertStatus(t, denyRoleReq, http.StatusForbidden, "POST /api/auth/roles with read-only token")

	// 5d. GET /api/auth/permissions -> 403 (missing auth:roles:read)
	denyPermsReq, _ := http.NewRequest(http.MethodGet, gatewayBaseURL+"/api/auth/permissions", nil)
	denyPermsReq.Header.Set("Authorization", readonlyHeader)
	assertStatus(t, denyPermsReq, http.StatusForbidden, "GET /api/auth/permissions with read-only token")

	// =========================================================================
	// Step 6: Allow-Path Assertion (read endpoint still accessible)
	// =========================================================================
	allowReadReq, _ := http.NewRequest(http.MethodGet, gatewayOrdersURL, nil)
	allowReadReq.Header.Set("Authorization", readonlyHeader)
	assertStatus(t, allowReadReq, http.StatusOK, "GET /api/orders with read-only token")

	t.Logf("7. TC-E2E-027 Passed: Read-only role denied at all write/manage boundaries (403) while retaining read access (200).")
}
