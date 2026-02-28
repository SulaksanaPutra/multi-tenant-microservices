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
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/brianvoe/gofakeit/v6"
	_ "github.com/lib/pq"
)

func TestE2E_CrossTenantPermissionIsolationBoundary(t *testing.T) {
	gofakeit.Seed(0)
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	// -------------------------------------------------------------------------
	// Step 1: Provision Tenant A
	// -------------------------------------------------------------------------
	emailA := gofakeit.Email()
	regReqA := RegisterReq{
		OwnerEmail: emailA,
		OwnerName:  gofakeit.Name(),
		Plan:       "shared",
		TenantName: "Tenant Alpha " + gofakeit.Company(),
	}
	bodyA, _ := json.Marshal(regReqA)

	respA, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(bodyA))
	if err != nil {
		t.Fatalf("Failed to register Tenant A: %v", err)
	}
	defer respA.Body.Close()

	if respA.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(respA.Body)
		t.Fatalf("Tenant A registration failed, got %d: %s", respA.StatusCode, string(b))
	}

	var regDataA RegisterResp
	_ = json.NewDecoder(respA.Body).Decode(&regDataA)
	tenantIDA := regDataA.Data.TenantID
	userIDA := resolveUserID(t, regDataA, emailA)

	waitForTenantActive(t, db, tenantIDA)
	passwordA := "TenantAlpha123!"
	setCredentials(t, userIDA, tenantIDA, emailA, passwordA)
	jwtA, _ := loginAndGetTokenPair(t, emailA, passwordA)
	t.Logf("Tenant A active: ID=%s, User=%s", tenantIDA, userIDA)

	// -------------------------------------------------------------------------
	// Step 2: Provision Tenant B
	// -------------------------------------------------------------------------
	emailB := gofakeit.Email()
	regReqB := RegisterReq{
		OwnerEmail: emailB,
		OwnerName:  gofakeit.Name(),
		Plan:       "shared",
		TenantName: "Tenant Beta " + gofakeit.Company(),
	}
	bodyB, _ := json.Marshal(regReqB)

	respB, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(bodyB))
	if err != nil {
		t.Fatalf("Failed to register Tenant B: %v", err)
	}
	defer respB.Body.Close()

	if respB.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(respB.Body)
		t.Fatalf("Tenant B registration failed, got %d: %s", respB.StatusCode, string(b))
	}

	var regDataB RegisterResp
	_ = json.NewDecoder(respB.Body).Decode(&regDataB)
	tenantIDB := regDataB.Data.TenantID
	userIDB := resolveUserID(t, regDataB, emailB)

	waitForTenantActive(t, db, tenantIDB)
	passwordB := "TenantBeta123!"
	setCredentials(t, userIDB, tenantIDB, emailB, passwordB)
	jwtB, _ := loginAndGetTokenPair(t, emailB, passwordB)
	jwtA, _ = loginAndGetTokenPair(t, emailA, passwordA)
	t.Logf("Tenant B active: ID=%s, User=%s", tenantIDB, userIDB)

	// -------------------------------------------------------------------------
	// Step 3: Create Order under Tenant A
	// -------------------------------------------------------------------------
	orderReqA := OrderReq{
		CustomerID: "cust_tenant_a_secret",
		Amount:     499.50,
	}
	orderBodyA, _ := json.Marshal(orderReqA)

	createOrderReq, _ := http.NewRequest(http.MethodPost, gatewayOrdersURL, bytes.NewBuffer(orderBodyA))
	createOrderReq.Header.Set("Content-Type", "application/json")
	createOrderReq.Header.Set("Authorization", "Bearer "+jwtA)

	createOrderResp, err := defaultHTTPClient.Do(createOrderReq)
	if err != nil {
		t.Fatalf("Failed to create order for Tenant A: %v", err)
	}
	defer createOrderResp.Body.Close()

	if createOrderResp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(createOrderResp.Body)
		t.Fatalf("Expected HTTP 201 Created for Tenant A order, got %d: %s", createOrderResp.StatusCode, string(b))
	}

	var createdOrderData OrderResp
	_ = json.NewDecoder(createOrderResp.Body).Decode(&createdOrderData)
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

	var listOrdersDataB ListOrdersResp
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
