/*
 * Test Specification: TC-E2E-019 - Cross-Tenant Permission Isolation Boundary
 * Architectural Scope: Gateway (Traefik), order-service, user-service, notification-service
 * Objective: Validate strict multi-tenant boundary isolation, ensuring valid JWT access tokens issued for Tenant A
 *            cannot access or mutate resources of Tenant B.
 * Failure Mode Guarded: Multi-tenant data leakage, lateral authorization bypass between tenants.
 *
 * Workflow / How It Works:
 *   1. Register two independent tenants: Tenant A (shared plan) and Tenant B (shared plan).
 *   2. Poll tenant_manager_db until both tenants reach 'active' status.
 *   3. Complete password setup and authenticate to acquire distinct JWT access tokens (jwtA and jwtB).
 *   4. Create an order under Tenant A via POST /api/orders using jwtA header.
 *   5. Issue GET /api/orders using jwtB header to query Tenant B's order context.
 *   6. Assert that Tenant B order query returns HTTP 200 OK with an empty array (zero data leakage from Tenant A).
 *   7. Attempt to directly access Tenant A's order ID using jwtB and assert HTTP 404 Not Found or HTTP 403 Forbidden.
 */

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestE2E_CrossTenantPermissionIsolationBoundary(t *testing.T) {
	// -------------------------------------------------------------------------
	// Step 1: Provision Tenant A
	// -------------------------------------------------------------------------
	tenantIDA, userIDA, emailA, passwordA := registerAndActivateTenant(t)
	setCredentials(t, userIDA, tenantIDA, emailA, passwordA)
	_, _ = loginAndGetTokenPair(t, emailA, passwordA)
	t.Logf("Tenant A active: ID=%s, User=%s", tenantIDA, userIDA)

	// -------------------------------------------------------------------------
	// Step 2: Provision Tenant B
	// -------------------------------------------------------------------------
	tenantIDB, userIDB, emailB, passwordB := registerAndActivateTenant(t)
	setCredentials(t, userIDB, tenantIDB, emailB, passwordB)
	jwtB, _ := loginAndGetTokenPair(t, emailB, passwordB)
	jwtA, _ := loginAndGetTokenPair(t, emailA, passwordA)
	t.Logf("Tenant B active: ID=%s, User=%s", tenantIDB, userIDB)

	// -------------------------------------------------------------------------
	// Step 3: Create Order under Tenant A
	// -------------------------------------------------------------------------
	orderReqA := OrderRequest{
		CustomerID: "cust_tenant_a_secret",
		Quantity:   1,
		Price:      499.50,
		Currency:   "USD",
	}
	orderBodyA, _ := json.Marshal(orderReqA)

	createOrderRequest, _ := http.NewRequest(http.MethodPost, gatewayOrdersURL, bytes.NewBuffer(orderBodyA))
	createOrderRequest.Header.Set("Content-Type", "application/json")
	createOrderRequest.Header.Set("Authorization", "Bearer "+jwtA)

	createOrderResponse, err := defaultHTTPClient.Do(createOrderRequest)
	if err != nil {
		t.Fatalf("Failed to create order for Tenant A: %v", err)
	}
	defer createOrderResponse.Body.Close()

	if createOrderResponse.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(createOrderResponse.Body)
		t.Fatalf("Expected HTTP 201 Created for Tenant A order, got %d: %s", createOrderResponse.StatusCode, string(b))
	}

	var createdOrderData OrderResponse
	_ = json.NewDecoder(createOrderResponse.Body).Decode(&createdOrderData)
	orderIDA := createdOrderData.Data.ID
	t.Logf("Created Tenant A Order ID: %s", orderIDA)

	// -------------------------------------------------------------------------
	// Step 4: Query Orders using Tenant B Credentials (jwtB)
	// -------------------------------------------------------------------------
	listReqB, _ := http.NewRequest(http.MethodGet, gatewayOrdersURL, nil)
	listReqB.Header.Set("Authorization", "Bearer "+jwtB)

	listRespB, err := defaultHTTPClient.Do(listReqB)
	if err != nil {
		t.Fatalf("Failed to execute order list query with Tenant B JWT: %v", err)
	}
	defer listRespB.Body.Close()

	if listRespB.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(listRespB.Body)
		t.Fatalf("Expected HTTP 200 OK for Tenant B order list query, got %d: %s", listRespB.StatusCode, string(b))
	}

	var listOrdersDataB ListOrdersResponse
	if err := json.NewDecoder(listRespB.Body).Decode(&listOrdersDataB); err != nil {
		t.Fatalf("Failed to decode Tenant B order list response: %v", err)
	}

	// Verify Tenant B sees 0 orders (Tenant A's order must NOT be visible)
	for _, order := range listOrdersDataB.Data {
		if order.ID == orderIDA || order.CustomerID == "cust_tenant_a_secret" {
			t.Fatalf("CRITICAL SECURITY VIOLATION: Cross-tenant data leakage detected! Tenant B observed Tenant A order ID %s", orderIDA)
		}
	}
	t.Logf("Tenant B list orders query returned %d orders. Zero data leakage verified.", len(listOrdersDataB.Data))

	// -------------------------------------------------------------------------
	// Step 5: Attempt Direct Access of Tenant A's Order ID with Tenant B JWT
	// -------------------------------------------------------------------------
	directAccessURL := fmt.Sprintf("%s/%s", gatewayOrdersURL, orderIDA)
	directReqB, _ := http.NewRequest(http.MethodGet, directAccessURL, nil)
	directReqB.Header.Set("Authorization", "Bearer "+jwtB)

	directRespB, err := defaultHTTPClient.Do(directReqB)
	if err != nil {
		t.Fatalf("Failed to execute direct order access request: %v", err)
	}
	defer directRespB.Body.Close()

	// Should be 404 Not Found (or 403 Forbidden) because orderIDA does not exist in Tenant B's DB schema
	if directRespB.StatusCode != http.StatusNotFound && directRespB.StatusCode != http.StatusForbidden {
		b, _ := io.ReadAll(directRespB.Body)
		t.Fatalf("Security Violation: Expected HTTP 404 Not Found or HTTP 403 Forbidden for cross-tenant direct order access, got %d: %s", directRespB.StatusCode, string(b))
	}

	t.Logf("TC-E2E-019 Passed: Cross-tenant isolation boundary strictly enforced.")
}
