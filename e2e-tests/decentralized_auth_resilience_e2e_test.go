/*
 * Test Specification: TC-E2E-015 - Decentralized Authorization Resilience (Auth Service Outage)
 * Architectural Scope: order-service (JWT Middleware), auth-service, Docker Daemon runtime
 * Objective: Verify that downstream domain services (order-service) validate RS256 JWT access tokens locally in-memory
 *            without making synchronous network calls to auth-service, remaining fully operational during auth-service outages.
 * Failure Mode Guarded: Centralized auth server single-point-of-failure bottlenecks and service cascading failures.
 *
 * Workflow / How It Works:
 *   1. Register a tenant, provision credentials via setup token flow, and obtain a valid RS256 JWT access token.
 *   2. Use Docker runtime command (`docker stop auth-service`) to simulate a complete auth-service outage.
 *   3. Confirm auth-service is completely unreachable via HTTP health check.
 *   4. Issue POST /api/orders with the valid JWT bearer token and assert HTTP 201 Created success.
 *   5. Restore auth-service container (`docker start auth-service`) and confirm health check recovery.
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

func TestE2E_TC_E2E_015_DecentralizedAuthorizationResilience(t *testing.T) {
	t.Log("=== TC-E2E-015: Decentralized Authorization Resilience (Auth Service Outage) ===")

	// =========================================================================
	// Step 1: Register Tenant & Provision User Credentials
	// Instruction: Issue Gateway POST /api/register request, await activation, and
	//              provision user password credentials.
	// =========================================================================
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	t.Logf("1. Registering tenant: owner='%s', email='%s', plan='shared'", ownerName, ownerEmail)

	regBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(regBody))
	if err != nil {
		t.Fatalf("POST /api/register failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP 202 Accepted, got %d", resp.StatusCode)
	}

	var regResp RegisterResp
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("Failed to decode register response: %v", err)
	}
	tenantID := regResp.Data.TenantID

	// Wait for tenant activation in database
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	waitForTenantActive(t, db, tenantID)
	t.Logf("2. Tenant '%s' is active.", tenantID)

	userID := resolveUserID(t, regResp, ownerEmail)

	// =========================================================================
	// Step 2: Authenticate User & Obtain Access Token
	// Instruction: Perform login via POST /auth/login to obtain valid 15-minute access token.
	// =========================================================================
	const password = "SecurePassword123!"
	setCredentials(t, userID, tenantID, ownerEmail, password)
	accessToken := loginAndGetToken(t, ownerEmail, password)
	t.Log("3. Credentials provisioned and login successful.")

	// Safety cleanup fixture ensuring auth-service container is restored even if test fails
	defer func() {
		t.Log("[Teardown] Ensuring auth-service container is running...")
		_ = exec.Command("docker", "start", "auth-service").Run()
	}()

	// =========================================================================
	// Step 3: Simulate Auth Service Outage (Stop Container)
	// Instruction: Stop auth-service container via system docker CLI and verify unreachability.
	// Architectural Invariant: Downstream order-service must retain RSA public key in memory.
	// =========================================================================
	t.Log("4. Stopping auth-service container to simulate auth outage...")
	if out, err := exec.Command("docker", "stop", "auth-service").CombinedOutput(); err != nil {
		t.Fatalf("Failed to stop auth-service container: %v (%s)", err, string(out))
	}

	// Confirm auth-service is unreachable before issuing downstream request
	authUp, _ := defaultHTTPClient.Get(authServiceURL + "/health")
	if authUp != nil && authUp.StatusCode == http.StatusOK {
		authUp.Body.Close()
		t.Fatal("auth-service is still responding after docker stop — cannot proceed with outage test")
	}
	t.Log("4. auth-service container stopped and confirmed unreachable.")

	// =========================================================================
	// Step 4: Issue Authenticated Domain Request During Auth Outage
	// Instruction: Issue POST /api/orders targeting order-service while auth-service is offline.
	// Architectural Invariant: Order service validates JWT mathematically using cached RS256 key
	//                          and processes the order independently (HTTP 201 Created).
	// =========================================================================
	orderBody, _ := json.Marshal(OrderReq{
		CustomerID: "cust_resilience_test",
		Amount:     199.99,
	})
	orderReq, err := http.NewRequest(http.MethodPost, gatewayOrdersURL, bytes.NewBuffer(orderBody))
	if err != nil {
		t.Fatalf("Failed to create POST /api/orders request: %v", err)
	}
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("Authorization", bearerHeader(accessToken))

	respOrder, err := defaultHTTPClient.Do(orderReq)
	if err != nil {
		t.Fatalf("POST /api/orders failed during auth outage: %v", err)
	}
	defer respOrder.Body.Close()

	if respOrder.StatusCode != http.StatusCreated {
		t.Fatalf("Expected HTTP 201 Created for order during auth outage, got %d", respOrder.StatusCode)
	}
	t.Log("5. Success: order-service processed the request (HTTP 201 Created) using local RS256 verification while auth-service was unreachable!")

	// =========================================================================
	// Step 5: Restore Auth Service & Verify Container Recovery
	// Instruction: Restart auth-service container and poll /health endpoint until HTTP 200 OK.
	// =========================================================================
	t.Log("6. Restarting auth-service container...")
	if err := exec.Command("docker", "start", "auth-service").Run(); err != nil {
		t.Logf("Warning: Failed to restart auth-service: %v", err)
	}

	restarted := false
	for i := 0; i < 20; i++ {
		hResp, err := defaultHTTPClient.Get(authServiceURL + "/health")
		if err == nil && hResp.StatusCode == http.StatusOK {
			hResp.Body.Close()
			restarted = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !restarted {
		t.Fatalf("auth-service did not recover on /health within 10 seconds after restart")
	}
	t.Log("6. auth-service container restarted and healthy.")
}
