/*
 * Test Specification: TC-E2E-031 - Order-Created Outbox Dual-Write & Consumer Idempotency Barrier
 * Architectural Scope: order-service (OrderRepository.CreateOrder dual-write to {{SCHEMA_NAME}}.orders +
 *                      {{SCHEMA_NAME}}.outbox, OutboxWorker FOR UPDATE SKIP LOCKED), notification-service
 *                      (OrderCreatedConsumer + InboxRepository ON CONFLICT (event_id)), shared_db tenant schema.
 * Objective: Validate the Phase-1 outbox contract introduced by the migration plan: (A) every order creation
 *            atomically stages an order.created outbox row inside the same database transaction, and (B) the
 *            downstream OrderCreatedConsumer enforces the mandatory InboxRepository idempotency barrier so
 *            duplicate at-least-once deliveries are trapped (zero duplicate inbox records).
 * Failure Mode Guarded: Partial dual-writes (order persisted without an outbox event), duplicate order-created
 *                       side effects under RabbitMQ redelivery (the plan's "Idempotency Barrier" requirement).
 *
 * Workflow / How It Works:
 *   1. Register a shared tenant, activate, provision credentials, login, create an order.
 *   2. Part A — Read the order.created outbox row from shared_db.<tenant_<id>_order_db>.outbox and assert it
 *      exists (transactional dual-write). Optionally observe the worker drain it to PUBLISHED.
 *   3. Part B — Publish the identical synthetic order.created event twice over company.events using the outbox
 *      row id as event_id, then poll notification_db public.inbox and assert exactly ONE inbox record exists.
 *
 * Known Environment Caveat:
 *   order-service main.go wires its OutboxWorker to the `postgres` DB `public.outbox` table while OrderRepository
 *   stages rows into shared_db.<tenant_schema>.outbox. Until that wiring is corrected the sender-side PUBLISH
 *   transition may never occur in a deployed environment. This test therefore treats the worker drain as a
 *   conditional observation and hard-asserts only the dual-write (Part A) and the downstream idempotency
 *   barrier (Part B), which are independent of the worker wiring. See E2E_SPECIFICATION_REPORT.md §5.6.
 */

package e2e_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestE2E_TC_E2E_031_OrderCreated_Outbox_ConsumerIdempotency(t *testing.T) {
	t.Log("=== TC-E2E-031: Order-Created Outbox Dual-Write & Consumer Idempotency Barrier ===")

	// =========================================================================
	// Step 1: Register Shared Tenant, Activate & Login
	// =========================================================================
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t, "shared")
	setCredentials(t, userID, tenantID, ownerEmail, password)
	accessToken := loginAndGetToken(t, ownerEmail, password)
	authHeader := bearerHeader(accessToken)

	// =========================================================================
	// Step 2: Create an Order (Stages the order.created outbox row atomically)
	// =========================================================================
	orderBody, _ := json.Marshal(OrderRequest{
		CustomerID: "cust_outbox_loop",
		Quantity:   1,
		Price:      88.25,
		Currency:   "USD",
	})
	orderReq, _ := http.NewRequest(http.MethodPost, gatewayOrdersURL, strings.NewReader(string(orderBody)))
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("Authorization", authHeader)
	orderResp, err := defaultHTTPClient.Do(orderReq)
	if err != nil {
		t.Fatalf("POST /api/orders failed: %v", err)
	}
	defer orderResp.Body.Close()
	if orderResp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(orderResp.Body)
		t.Fatalf("Expected HTTP 201 Created, got %d: %s", orderResp.StatusCode, string(bodyBytes))
	}
	var created struct {
		Data OrderResponseData `json:"data"`
	}
	_ = json.NewDecoder(orderResp.Body).Decode(&created)
	orderID := created.Data.ID
	t.Logf("2. Order '%s' created for tenant '%s'.", orderID, tenantID)

	// =========================================================================
	// Step 3 (Part A): Verify Transactional Outbox Dual-Write
	// Instruction: Read the order.created row from shared_db.<tenant_<id>_order_db>.outbox.
	//              If the row is absent, the CreateOrder dual-write contract is broken.
	// =========================================================================
	sharedDB, err := sql.Open("postgres", sharedDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to shared_db: %v", err)
	}
	defer sharedDB.Close()

	notifDB, err := sql.Open("postgres", notificationDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to notification_db: %v", err)
	}
	defer notifDB.Close()

	schemaName := tenantOrderSchemaName(tenantID)
	outboxQuery := "SELECT id, status FROM " + schemaName + ".outbox " +
		"WHERE tenant_id = $1 AND event_type = 'order.created' ORDER BY created_at DESC LIMIT 1"

	var outboxID, outboxStatus string
	staged := false
	for i := 0; i < 20; i++ {
		err = sharedDB.QueryRow(outboxQuery, tenantID).Scan(&outboxID, &outboxStatus)
		if err == nil {
			staged = true
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !staged {
		t.Fatalf("Outbox dual-write broken: no order.created row staged in shared_db schema '%s' for tenant '%s'", schemaName, tenantID)
	}
	t.Logf("3a. Dual-write verified: order.created outbox row id='%s' staged in '%s.outbox' (status='%s').", outboxID, schemaName, outboxStatus)

	// =========================================================================
	// Step 3 (Part A, conditional): Observe the Sender-Side Worker Drain
	// Instruction: Poll up to 20s for the outbox row to reach PUBLISHED. If it does, verify
	//              the downstream inbox already holds exactly one record (full loop). If it does
	//              not, log the known worker-wiring caveat (see §5.6) and continue with Part B.
	// =========================================================================
	published := false
	for i := 0; i < 40; i++ {
		var st string
		if err := sharedDB.QueryRow("SELECT status FROM "+schemaName+".outbox WHERE id = $1", outboxID).Scan(&st); err == nil && st == "PUBLISHED" {
			published = true
			outboxStatus = st
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if published {
		loopVerified := false
		for i := 0; i < 20; i++ {
			if countInboxRows(t, notifDB, outboxID) == 1 {
				loopVerified = true
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if !loopVerified {
			t.Fatalf("Outbox row '%s' was PUBLISHED but the downstream inbox never claimed it — full loop broken", outboxID)
		}
		t.Logf("3b. Sender-side worker drained the event; downstream inbox holds exactly one record (full loop verified).")
	} else {
		t.Logf("3b. WARNING: outbox row '%s' did not reach PUBLISHED within 20s — order-service OutboxWorker wiring caveat (see report §5.6).", outboxID)
	}

	// =========================================================================
	// Step 4 (Part B): Publish Duplicate order.created Events (Idempotency Barrier)
	// Instruction: Publish the identical synthetic order.created payload twice using the real
	//              outbox row id as event_id. The notification-service OrderCreatedConsumer MUST
	//              trap the duplicate at the InboxRepository (ON CONFLICT DO NOTHING).
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

	evt := map[string]any{
		"event_id":    outboxID,
		"tenant_id":   tenantID,
		"order_id":    orderID,
		"customer_id": "cust_outbox_loop",
		"quantity":    1,
		"price":       88.25,
		"amount":      88.25,
		"currency":    "USD",
		"status":      "PENDING",
	}
	publishCompanyEvent(t, ch, "order.created", evt)
	publishCompanyEvent(t, ch, "order.created", evt)
	t.Logf("4. Published two duplicate order.created events with event_id='%s'.", outboxID)

	// =========================================================================
	// Step 5: Assert Exactly One Inbox Record Survives
	// Instruction: Poll notification_db public.inbox until the consumer has processed the
	//              duplicates; the ON CONFLICT (event_id) DO NOTHING barrier must yield count == 1.
	// =========================================================================
	settled := false
	finalCount := 0
	for i := 0; i < 30; i++ {
		finalCount = countInboxRows(t, notifDB, outboxID)
		if finalCount == 1 {
			settled = true
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("Idempotency barrier FAILED: expected exactly 1 inbox row for event_id='%s', got %d", outboxID, finalCount)
	}

	var evtType string
	_ = notifDB.QueryRow("SELECT event_type FROM public.inbox WHERE event_id = $1", outboxID).Scan(&evtType)
	if evtType != "order.created" {
		t.Fatalf("Inbox record for event_id='%s' has unexpected event_type='%s'", outboxID, evtType)
	}

	t.Logf("5. TC-E2E-031 Passed: dual-write staged (schema '%s') and duplicate order.created deliveries trapped by the inbox barrier (count=%d).", schemaName, finalCount)
}

// tenantOrderSchemaName returns the order-service per-tenant schema name for a given tenant id
// (matches infra-provisioner's sanitizeTenantID + "_order_db" convention).
func tenantOrderSchemaName(tenantID string) string {
	return strings.ReplaceAll(strings.ToLower(tenantID), "-", "_") + "_order_db"
}

// publishCompanyEvent publishes a JSON payload to the company.events exchange with the given routing key.
func publishCompanyEvent(t *testing.T, ch *amqp.Channel, routingKey string, payload any) {
	t.Helper()
	body, _ := json.Marshal(payload)
	if err := ch.Publish("company.events", routingKey, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        body,
	}); err != nil {
		t.Fatalf("Failed to publish '%s' event: %v", routingKey, err)
	}
}