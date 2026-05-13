/*
 * Test Specification: TC-E2E-029 - Order-Service (Data-Plane Consumer) Outage Recovery
 * Architectural Scope: order-service (InfrastructureProvisionedConsumer, durable queue
 *                      order_service_infra_provisioned, PoolRegistry bootstrap), tenant-service
 *                      (TenantOrderDBReadyConsumer), infra-provisioner, Traefik Gateway.
 * Objective: Validate fault tolerance of the DATA-PLANE consumer path. While order-service is offline,
 *            infrastructure.provisioned events must accumulate safely in a durable RabbitMQ queue, the tenant
 *            must remain pending (NOT active), and upon container restart order-service must drain the queue,
 *            publish tenant.order_db.ready, and the tenant must activate with orders fully functional.
 * Failure Mode Guarded: Lost routing/activation events during order-service crashes, broken PoolRegistry
 *                       bootstrap after container restart, zombie 'pending' tenants after recovery.
 *
 * Workflow / How It Works:
 *   1. Stop order-service container via system Docker CLI.
 *   2. Submit registration request POST /api/tenants/register while order-service is offline.
 *   3. Poll tenant_manager_db and assert the tenant does NOT reach 'active' (activation is blocked on the
 *      order_db.ready publication that only order-service can emit).
 *   4. Restart order-service container via system Docker CLI.
 *   5. Poll tenant_manager_db until tenant transitions to 'active' (queue drained, DSN bootstrapped).
 *   6. Provision credentials, login, and issue POST /api/orders asserting HTTP 201 Created.
 *
 * Operational Preconditions:
 *   - Requires host Docker CLI access with permission to stop/start the `order-service` container.
 *   - The durable queue `order_service_infra_provisioned` MUST already exist in RabbitMQ (declared by a prior
 *     order-service start via its setupTopology). If order-service has never started against a fresh RabbitMQ,
 *     published `infrastructure.provisioned` events are silently dropped (mandatory=false) and the tenant would
 *     never activate, causing this test to fail.
 *   - MUST run serially (`go test -p 1`); stopping order-service while other tests execute will break them.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"os/exec"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestE2E_TC_E2E_029_OrderServiceOutage_Recovery(t *testing.T) {
	t.Log("=== TC-E2E-029: Order-Service (Data-Plane) Outage & Queue Catch-Up Recovery ===")

	// =========================================================================
	// Step 1: Simulate Data-Plane Consumer Outage (Stop order-service)
	// Instruction: Stop order-service container via system Docker CLI.
	// Architectural Invariant: infrastructure.provisioned events accumulate in the durable
	//                          order_service_infra_provisioned queue while the consumer is offline.
	// =========================================================================
	t.Log("1. Stopping order-service container to simulate data-plane consumer outage...")
	stopCmd := exec.Command("docker", "stop", "order-service")
	if out, err := stopCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to stop order-service: %v (%s)", err, string(out))
	}

	// Teardown fixture ensuring order-service is restarted even on test failure.
	defer func() {
		_ = exec.Command("docker", "start", "order-service").Run()
	}()

	// =========================================================================
	// Step 2: Submit Registration During Order-Service Outage
	// Instruction: Submit POST /api/tenants/register while order-service is offline.
	// =========================================================================
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	reqBody, _ := json.Marshal(RegisterRequest{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("Failed to submit registration during order-service outage: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP 202 Accepted during outage, got %d", resp.StatusCode)
	}

	var regResp RegisterResponse
	_ = json.NewDecoder(resp.Body).Decode(&regResp)

	tenantID := resolveTenantID(t, ownerEmail)
	t.Logf("2. Registration accepted while order-service is offline (tenant_id='%s').", tenantID)

	// =========================================================================
	// Step 3: Assert Tenant Does NOT Activate While Order-Service Is Offline
	// Instruction: Poll tenant_manager_db for up to 8 seconds and assert status stays non-active.
	// Architectural Invariant: Activation depends on tenant.order_db.ready, published exclusively
	//                          by order-service. While it is down, the tenant must remain 'pending'.
	// =========================================================================
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	var tenantStatus string
	for i := 0; i < 16; i++ {
		err = db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&tenantStatus)
		if err == nil && tenantStatus == "active" {
			t.Fatalf("Premature activation: tenant '%s' became active while order-service was offline!", tenantID)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("3. Verified tenant remains '%s' while order-service is offline (activation blocked on data-plane consumer).", tenantStatus)

	// =========================================================================
	// Step 4: Restore Order-Service Container
	// Instruction: Restart order-service container via system Docker CLI.
	// =========================================================================
	t.Log("4. Restarting order-service container...")
	startCmd := exec.Command("docker", "start", "order-service")
	if out, err := startCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to restart order-service: %v (%s)", err, string(out))
	}

	// =========================================================================
	// Step 5: Verify Tenant Activation After Consumer Recovery
	// Instruction: Poll tenant_manager_db until the buffered infrastructure.provisioned event is
	//              drained, order_db.ready is published, and tenant reaches 'active'.
	// =========================================================================
	activated := false
	for i := 0; i < 120; i++ {
		err := db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&tenantStatus)
		if err == nil && tenantStatus == "active" {
			activated = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !activated {
		t.Fatalf("Order-service recovery failed: tenant '%s' did not reach 'active' after restart. Final status: '%s'", tenantID, tenantStatus)
	}
	t.Logf("5. Verified tenant '%s' activated after order-service recovery (queue drained, DSN bootstrapped).", tenantID)

	// =========================================================================
	// Step 6: Verify Data-Plane Functionality After Recovery
	// Instruction: Resolve user_id, provision credentials, login, and create an order.
	// =========================================================================
	userID := resolveUserID(t, regResp, ownerEmail)
	const e2ePassword = "e2e-order-outage-123"
	setCredentials(t, userID, tenantID, ownerEmail, e2ePassword)
	accessToken := loginAndGetToken(t, ownerEmail, e2ePassword)

	orderBody, _ := json.Marshal(OrderRequest{CustomerID: "cust_post_recovery", Amount: 77.50})
	orderReq, _ := http.NewRequest(http.MethodPost, gatewayOrdersURL, bytes.NewBuffer(orderBody))
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("Authorization", bearerHeader(accessToken))

	orderResp, err := defaultHTTPClient.Do(orderReq)
	if err != nil {
		t.Fatalf("POST /api/orders after recovery failed: %v", err)
	}
	defer orderResp.Body.Close()

	if orderResp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected HTTP 201 Created after order-service recovery, got %d", orderResp.StatusCode)
	}

	t.Logf("6. TC-E2E-029 Passed: Data-plane consumer outage survived; tenant activated and orders served post-recovery.")
}
