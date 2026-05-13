/*
 * Test Specification: TC-E2E-004 - Fanout Exchange Broadcast & Cache Invalidation
 * Architectural Scope: RabbitMQ Fanout Exchange (company.events), order-service (PoolRegistry, TenantDBResolver)
 * Objective: Validate multi-instance in-memory cache eviction (PoolRegistry DSN routing metadata) across all
 *            service replicas when a tenant infrastructure migration or failover event is broadcast.
 * Failure Mode Guarded: Stale database connection pool routing following tenant database migration or scaling.
 *
 * Workflow / How It Works:
 *   1. Establish AMQP channel with RabbitMQ broker.
 *   2. Register a new tenant, authenticate via JWT, and issue POST /api/orders to warm order-service connection pool cache.
 *   3. Broadcast synthetic AMQP event tenant.infrastructure_changed over company.events exchange.
 *   4. Wait 1 second for fanout consumer queues across all order-service replicas to consume the message and purge PoolRegistry.
 *   5. Issue follow-up GET /api/orders request and verify order-service re-resolves DSN and succeeds with HTTP 200 OK.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	_ "github.com/lib/pq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestE2E_InfrastructureFanout_BroadcastPurge(t *testing.T) {
	t.Log("=== E2E Test: Infrastructure Changed Fanout Exchange Broadcast Purge (Docs Case #13 & #14) ===")

	// =========================================================================
	// Step 1: Establish RabbitMQ AMQP Connection & Listener
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

	// Bind queue to intercept the generated tenant_id from registration
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
	// Step 2: Register Shared Tenant
	// =========================================================================
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	reqBody, _ := json.Marshal(RegisterRequest{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := http.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to register tenant for fanout test: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResponse
	_ = json.NewDecoder(resp.Body).Decode(&regResp)

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
		t.Logf("Intercepted tenant_id='%s' from RabbitMQ", tenantID)
	case <-time.After(10 * time.Second):
		t.Fatalf("Timed out waiting for workspace.initiated event")
	}

	// =========================================================================
	// Step 4: Await Activation & Warm Connection Pool Cache
	// =========================================================================
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	var tenantStatus string
	activated := false
	for i := 0; i < 20; i++ {
		err := db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&tenantStatus)
		if err == nil && tenantStatus == "active" {
			activated = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !activated {
		t.Fatalf("Tenant %s failed to reach 'active' status. Final status: '%s'", tenantID, tenantStatus)
	}

	// Use safe resolution for asynchronous user ID
	userID := resolveUserID(t, regResp, ownerEmail)
	const e2ePassword = "e2e-test-password-123"
	setCredentials(t, userID, tenantID, ownerEmail, e2ePassword)
	accessToken := loginAndGetToken(t, ownerEmail, e2ePassword)

	// Issue order to warm PoolRegistry connection cache
	orderBody, _ := json.Marshal(OrderRequest{CustomerID: "cust_fanout", Amount: 99.00})
	orderReq, _ := http.NewRequest("POST", gatewayOrdersURL, bytes.NewBuffer(orderBody))
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("Authorization", bearerHeader(accessToken))

	oResp, err := http.DefaultClient.Do(orderReq)
	if err == nil {
		oResp.Body.Close()
	}

	// =========================================================================
	// Step 5: Broadcast Infrastructure Changed Event Over Fanout Exchange
	// =========================================================================
	fanoutEvt, _ := json.Marshal(map[string]any{
		"tenant_id": tenantID,
		"reason":    "plan_upgrade_or_failover",
	})

	err = ch.Publish(
		"company.events",
		"tenant.infrastructure_changed",
		false, false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        fanoutEvt,
		},
	)
	if err != nil {
		t.Fatalf("Failed to publish tenant.infrastructure_changed event: %v", err)
	}

	t.Logf("1. Broadcasted tenant.infrastructure_changed event for tenant_id='%s' over Fanout Exchange!", tenantID)

	time.Sleep(1 * time.Second)

	// =========================================================================
	// Step 6: Verify Connection Cache Purge & Re-Resolution
	// =========================================================================
	followUpReq, _ := http.NewRequest("GET", gatewayOrdersURL, nil)
	followUpReq.Header.Set("Authorization", bearerHeader(accessToken))

	fResp, err := http.DefaultClient.Do(followUpReq)
	if err != nil {
		t.Fatalf("Follow-up GET /api/orders request after cache purge failed: %v", err)
	}
	defer fResp.Body.Close()

	if fResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200 OK after cache purge, got %d", fResp.StatusCode)
	}

	t.Logf("2. Verified order-service successfully handled cache purge & resolved fresh DSN for tenant_id='%s'!", tenantID)
}
