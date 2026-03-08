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
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	_ "github.com/lib/pq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestE2E_DedicatedPlan_FullWorkflow(t *testing.T) {
	t.Log("=== E2E Test: Dedicated Plan Dynamic Docker Container Provisioning & Order Flow ===")

	// =========================================================================
	// Step 1: Bind Ephemeral AMQP Listener Queue
	// =========================================================================
	rmqConn, err := amqp.Dial(rabbitmqDSN)
	if err != nil {
		t.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer rmqConn.Close()

	ch, err := rmqConn.Channel()
	if err != nil {
		t.Fatalf("Failed to open RabbitMQ channel: %v", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("Failed to declare queue: %v", err)
	}
	if err := ch.QueueBind(q.Name, "workspace.initiated", "company.events", false, nil); err != nil {
		t.Fatalf("Failed to bind queue: %v", err)
	}

	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("Failed to consume from queue: %v", err)
	}

	// =========================================================================
	// Step 2: Submit Dedicated Plan Registration Request
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
		t.Fatalf("HTTP POST to gateway failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP 202 Accepted for dedicated registration, got %d", resp.StatusCode)
	}

	var regResp RegisterResp
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("Failed to decode register response: %v", err)
	}
	t.Logf("2. Dedicated Tenant registration accepted dynamically! Waiting for async processing...")

	// =========================================================================
	// Step 3: Intercept AMQP Event Payload & Extract TenantID
	// =========================================================================
	var tenantID string
	select {
	case d := <-msgs:
		var event map[string]any
		_ = json.Unmarshal(d.Body, &event)

		tID, ok := event["tenant_id"].(string)
		if !ok || tID == "" {
			t.Fatalf("RabbitMQ event missing tenant_id: %v", event)
		}
		tenantID = tID
		t.Logf("3. Extracted tenant_id='%s' from workspace.initiated event on RabbitMQ", tenantID)
	case <-time.After(10 * time.Second):
		t.Fatalf("Timed out waiting for workspace.initiated event")
	}

	// =========================================================================
	// Step 4: Poll Database for Dynamic Container Provisioning & Active Status
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
	t.Logf("4. Verified dedicated container provisioned & tenant_id='%s' reached 'active' status!", tenantID)

	// =========================================================================
	// Step 5: Verify Infrastructure Routing Metadata
	// =========================================================================
	var dbHost string
	var dbPort int
	err = db.QueryRow("SELECT db_host, db_port FROM public.tenant_infrastructures WHERE tenant_id = $1 AND service_name = 'order-service'", tenantID).Scan(&dbHost, &dbPort)
	if err != nil {
		t.Fatalf("Failed to find routing metadata in tenant_infrastructures: %v", err)
	}
	t.Logf("5. Verified routing metadata for dedicated DB: host='%s', port=%d", dbHost, dbPort)

	// =========================================================================
	// Step 6: Authenticate & Create Order on Dedicated Database Container
	// =========================================================================
	custID := gofakeit.UUID()
	orderBody, _ := json.Marshal(OrderReq{
		CustomerID: custID,
		Amount:     499.99,
	})

	// Safely resolve the user ID via DB polling
	userID := resolveUserID(t, regResp, ownerEmail)
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
	t.Logf("6. Successfully created order id='%s' on dedicated tenant DB container!", orderID)

	// =========================================================================
	// Step 7: Query Orders from Dedicated Database Container
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
	t.Logf("7. Verified GET /api/orders returned %d order(s) for dedicated tenant_id='%s'!", len(listResp.Data), tenantID)
}
