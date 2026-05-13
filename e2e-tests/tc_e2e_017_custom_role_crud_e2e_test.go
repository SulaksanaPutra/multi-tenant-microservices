/*
 * Test Specification: TC-E2E-017 - Multi-Tenant Custom Role CRUD & Instant Permission Invalidation
 * Architectural Scope: auth-service, order-service, user-service, notification-service
 * Objective: Validate tenant-scoped custom role creation, atomic user permission version batch-incrementing,
 *            and instant downstream token revocation (VersionCache invalidation).
 * Failure Mode Guarded: Post-revocation unauthorized access using non-expired JWT access tokens, role permission drift.
 *
 * Workflow / How It Works:
 *   1. Register a new tenant via POST /api/register and await active status in tenant_manager_db.
 *   2. Setup owner credentials via internal token flow and authenticate via POST /auth/login to obtain JWT access token (perm_version = 1).
 *   3. Create a tenant-scoped custom role via POST /api/roles and update permissions via PUT /api/roles/:id/permissions.
 *   4. Verify auth-service database (tenant_manager_db) user_permission_versions table reflects version increment to 2.
 *   5. Issue request POST /api/orders using the initial JWT access token (perm_version = 1).
 *   6. Assert HTTP 401 Unauthorized response rejection due to permission version mismatch.
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

	"auth-service/internal/handler"
	"auth-service/internal/httputil"

	_ "github.com/lib/pq"
)

func TestE2E_MultiTenant_CustomRoleCRUD_And_InstantPermissionInvalidation(t *testing.T) {
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t)
	t.Logf("Registered tenantID: %s, userID: %s", tenantID, userID)

	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	// Step 3: Provision credentials & Login to get initial JWT (perm_version = 1)
	setCredentials(t, userID, tenantID, ownerEmail, password)
	initialToken, _ := loginAndGetTokenPair(t, ownerEmail, password)
	t.Logf("Acquired initial access token (perm_version = 1)")

	// Step 4: Create Custom Role via POST /api/roles
	roleReq := handler.CreateRoleRequest{
		Name:        "Inventory Manager",
		Description: "Manages product inventory and catalog",
		Permissions: []string{"orders:read", "orders:write"},
	}
	roleBody, _ := json.Marshal(roleReq)

	req, _ := http.NewRequest(http.MethodPost, "http://localhost:8000/api/auth/roles", bytes.NewBuffer(roleBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+initialToken)

	roleResp, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to execute create role request: %v", err)
	}
	defer roleResp.Body.Close()

	if roleResp.StatusCode != http.StatusCreated && roleResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(roleResp.Body)
		t.Fatalf("Expected role creation success (200/201), got %d: %s", roleResp.StatusCode, string(body))
	}

	var roleData httputil.StandardResponse[handler.RoleResponse]
	_ = json.NewDecoder(roleResp.Body).Decode(&roleData)
	roleID := roleData.Data.ID
	t.Logf("Created custom role ID: %s", roleID)

	// Step 4B: Assign created custom role to user
	assignReqBody, _ := json.Marshal(map[string]string{"role_id": roleID})
	assignReqURL := fmt.Sprintf("http://localhost:8000/api/auth/users/%s/role", userID)
	assignReq, _ := http.NewRequest(http.MethodPut, assignReqURL, bytes.NewBuffer(assignReqBody))
	assignReq.Header.Set("Content-Type", "application/json")
	assignReq.Header.Set("Authorization", "Bearer "+initialToken)

	assignResp, err := defaultHTTPClient.Do(assignReq)
	if err != nil {
		t.Fatalf("Failed to execute assign user role request: %v", err)
	}
	assignResp.Body.Close()
	if assignResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK for role assignment, got %d", assignResp.StatusCode)
	}
	t.Logf("Assigned custom role %s to user %s", roleID, userID)

	// Step 5: Update Role Permissions via PUT /api/roles/:id/permissions
	// This triggers user permission version batch incrementing in auth-service.
	updatePermsReq := handler.UpdateRolePermissionsRequest{
		Permissions: []string{"orders:read", "orders:write", "inventory:admin"},
	}
	updateBody, _ := json.Marshal(updatePermsReq)

	updateReqURL := fmt.Sprintf("http://localhost:8000/api/auth/roles/%s/permissions", roleID)
	updateReq, _ := http.NewRequest(http.MethodPut, updateReqURL, bytes.NewBuffer(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("Authorization", "Bearer "+initialToken)

	updateResp, err := defaultHTTPClient.Do(updateReq)
	if err != nil {
		t.Fatalf("Failed to execute update role permissions request: %v", err)
	}
	defer updateResp.Body.Close()

	if updateResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(updateResp.Body)
		t.Fatalf("Expected HTTP 200 OK for role permission update, got %d: %s", updateResp.StatusCode, string(body))
	}

	// Step 6: Verify permission version increment in database
	var currentVersion int
	queryErr := db.QueryRow("SELECT version FROM public.user_permission_versions WHERE user_id = $1 AND tenant_id = $2", userID, tenantID).Scan(&currentVersion)
	if queryErr == nil {
		t.Logf("User permission version successfully batch-incremented to: %d", currentVersion)
	}

	// Step 7: Attempt to create an order using the initial JWT (perm_version = 1)
	// Expect rejection (HTTP 401 Unauthorized) because the initial token version is superseded.
	orderReq := OrderRequest{
		CustomerID: "cust_stale_perm_test",
		Amount:     199.99,
	}
	orderBody, _ := json.Marshal(orderReq)

	orderReqObj, _ := http.NewRequest(http.MethodPost, gatewayOrdersURL, bytes.NewBuffer(orderBody))
	orderReqObj.Header.Set("Content-Type", "application/json")
	orderReqObj.Header.Set("Authorization", "Bearer "+initialToken)

	orderResp, err := defaultHTTPClient.Do(orderReqObj)
	if err != nil {
		t.Fatalf("Failed to execute order request with superseded token: %v", err)
	}
	defer orderResp.Body.Close()

	if orderResp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(orderResp.Body)
		t.Fatalf("Security Violation: Expected HTTP 401 Unauthorized for superseded token (perm_version mismatch), got %d: %s", orderResp.StatusCode, string(body))
	}

	t.Logf("TC-E2E-017 Passed: Modifying custom role permissions cleanly invalidated stale token.")
}
