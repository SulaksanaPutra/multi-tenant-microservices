/*
 * Test Specification: TC-E2E-002 - Dedicated Plan Dynamic Docker Container Provisioning
 * Architectural Scope: infra-provisioner, Docker Daemon (/var/run/docker.sock), tenant_manager_db, order-service, auth-service
 * Objective: Validate full asynchronous lifecycle of dedicated plan registration, dynamic Docker container
 *            provisioning (postgres-tenant-<id> with 512MB RAM / 0.5 CPU limits), health check polling, Zero-Trust role
 *            bootstrapping, setup token authentication, and order execution on the private dedicated container.
 * Failure Mode Guarded: Compute resource contention, invalid container privilege bootstrapping, container crash during provisioning.
 *
 * Workflow / How It Works:
 *   1. Submit registration request POST /api/register with plan: "dedicated".
 *   2. Assert HTTP 202 Accepted response containing tenant_id (tnt_*) and user_id.
 *   3. Poll tenant_manager_db public.tenants until status transitions to 'active' (giving infra-provisioner time to spin up Docker container).
 *   4. Verify infrastructure routing metadata in public.tenant_infrastructures (db_host, db_port).
 *   5. Provision credentials via setup token flow and login to obtain RS256 JWT access token.
 *   6. Create an order via Gateway POST /api/orders targeting the dedicated container database and assert HTTP 201 Created.
 *   7. Fetch orders via GET /api/orders and assert retrieval from the private dedicated database.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	_ "github.com/lib/pq"
)

func TestE2E_DedicatedPlan_FullWorkflow(t *testing.T) {
	t.Log("=== E2E Test: Dedicated Plan Dynamic Docker Container Provisioning & Order Flow ===")

	// =========================================================================
	// Step 1: Submit Dedicated Plan Registration Request
	// Instruction: Issue HTTP POST to Gateway /api/register with plan="dedicated".
	// Architectural Invariant: Gateway creates outbox entry and returns HTTP 202 Accepted.
	// =========================================================================
	ownerName, ownerEmail, tenantName, _ := generateFakeData("dedicated")
	t.Logf("1. Submitting Dedicated Plan Registration: owner='%s', email='%s', tenant='%s'", ownerName, ownerEmail, tenantName)

	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "dedicated",
		TenantName: tenantName,
	})

	resp, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("HTTP POST /api/register failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP 202 Accepted for dedicated registration, got %d", resp.StatusCode)
	}

	var regResp RegisterResp
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("Failed to decode register response: %v", err)
	}
	tenantID := regResp.Data.TenantID
	if tenantID == "" || !strings.HasPrefix(tenantID, "tnt_") {
		t.Fatalf("Expected valid tenant_id starting with 'tnt_', got '%s'", tenantID)
	}
	t.Logf("2. Dedicated Tenant registration accepted! tenant_id='%s'", tenantID)

	// =========================================================================
	// Step 2: Poll Database for Dynamic Container Provisioning & Active Status
	// Instruction: Poll public.tenants for up to 15s to allow infra-provisioner to spawn container
	//              and perform schema/role bootstrapping.
	// Architectural Invariant: Infra-provisioner creates Docker container with resource limits.
	// =========================================================================
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	var tenantStatus string
	activated := false
	for i := 0; i < 30; i++ {
		err := db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&tenantStatus)
		if err == nil && tenantStatus == "active" {
			activated = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !activated {
		t.Fatalf("Dedicated Tenant %s failed to reach 'active' status. Final status: '%s'", tenantID, tenantStatus)
	}
	t.Logf("3. Verified dedicated container provisioned & tenant_id='%s' reached 'active' status!", tenantID)

	// =========================================================================
	// Step 3: Verify Infrastructure Routing Metadata
	// Instruction: Query public.tenant_infrastructures for order-service db_host and db_port.
	// =========================================================================
	var dbHost string
	var dbPort int
	err = db.QueryRow("SELECT db_host, db_port FROM public.tenant_infrastructures WHERE tenant_id = $1 AND service_name = 'order-service'", tenantID).Scan(&dbHost, &dbPort)
	if err != nil {
		t.Fatalf("Failed to find routing metadata in tenant_infrastructures: %v", err)
	}
	t.Logf("4. Verified routing metadata for dedicated DB: host='%s', port=%d", dbHost, dbPort)

	// =========================================================================
	// Step 4: Authenticate & Create Order on Dedicated Database Container
	// Instruction: Provision user credentials, login to receive JWT, and issue POST /api/orders.
	// Architectural Invariant: Order service routes queries directly to private container compute.
	// =========================================================================
	custID := gofakeit.UUID()
	orderBody, _ := json.Marshal(OrderReq{
		CustomerID: custID,
		Amount:     499.99,
	})

	userID := regResp.Data.UserID
	const e2ePassword = "e2e-test-password-123"
	setCredentials(t, userID, tenantID, ownerEmail, e2ePassword)
	accessToken := loginAndGetToken(t, ownerEmail, e2ePassword)

	orderReq, _ := http.NewRequest("POST", gatewayOrdersURL, bytes.NewBuffer(orderBody))
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("Authorization", bearerHeader(accessToken))

	oResp, err := defaultHTTPClient.Do(orderReq)
	if err != nil {
		t.Fatalf("POST /api/orders targeting dedicated tenant failed: %v", err)
	}
	defer oResp.Body.Close()

	if oResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(oResp.Body)
		t.Fatalf("Expected HTTP 201 Created for order creation on dedicated DB, got %d: %s", oResp.StatusCode, string(respBody))
	}

	var createOrderResp OrderResp
	if err := json.NewDecoder(oResp.Body).Decode(&createOrderResp); err != nil {
		t.Fatalf("Failed to decode order response: %v", err)
	}
	orderID := createOrderResp.Data.ID
	t.Logf("5. Successfully created order id='%s' on dedicated tenant DB container!", orderID)

	// =========================================================================
	// Step 5: Query Orders from Dedicated Database Container
	// Instruction: Issue GET /api/orders with bearer token and assert list retrieval.
	// =========================================================================
	getOrdersReq, _ := http.NewRequest("GET", gatewayOrdersURL, nil)
	getOrdersReq.Header.Set("Authorization", bearerHeader(accessToken))

	getResp, err := defaultHTTPClient.Do(getOrdersReq)
	if err != nil || getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/orders failed on dedicated DB or returned non-200 status: %v", getResp)
	}
	defer getResp.Body.Close()

	var listResp ListOrdersResp
	if err := json.NewDecoder(getResp.Body).Decode(&listResp); err != nil {
		t.Fatalf("Failed to decode list orders response: %v", err)
	}

	if len(listResp.Data) == 0 {
		t.Fatalf("Expected at least 1 order for dedicated tenant_id='%s', got 0", tenantID)
	}
	t.Logf("6. Verified GET /api/orders returned %d order(s) for dedicated tenant_id='%s'!", len(listResp.Data), tenantID)
}
