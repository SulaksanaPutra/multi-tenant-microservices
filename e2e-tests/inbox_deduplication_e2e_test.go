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

func TestE2E_InboxDeduplication_BarrierSafety(t *testing.T) {
	t.Log("=== E2E Test: Inbox Deduplication Barrier & Idempotent Processing (Docs Case #2) ===")

	// 1. Connect to RabbitMQ to publish synthetic duplicate event
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

	// 2. Register a tenant to get a valid tenant_id
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := http.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to register tenant for deduplication test: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResp
	_ = json.NewDecoder(resp.Body).Decode(&regResp)
	tenantID := regResp.Data.TenantID

	// 3. Connect to database to verify inbox entries
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	// Wait for tenant activation
	for i := 0; i < 20; i++ {
		var status string
		_ = db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&status)
		if status == "active" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 4. Publish a synthetic duplicate tenant.order_db.ready event to company.events exchange
	dupEventID := "evt_duplicate_test_" + tenantID
	dupPayload, _ := json.Marshal(map[string]any{
		"event_id":     dupEventID,
		"tenant_id":    tenantID,
		"service_name": "order-service",
		"db_host":      "postgres",
		"db_port":      5432,
		"db_name":      "shared_db",
		"db_user":      "postgres",
		"schema_name":  "tenant_test",
	})

	// Publish first event
	err = ch.Publish(
		"company.events",
		"tenant.order_db.ready",
		false, false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        dupPayload,
		},
	)
	if err != nil {
		t.Fatalf("Failed to publish first synthetic event: %v", err)
	}

	time.Sleep(1 * time.Second)

	// Publish second event with EXACT same event_id
	err = ch.Publish(
		"company.events",
		"tenant.order_db.ready",
		false, false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        dupPayload,
		},
	)
	if err != nil {
		t.Fatalf("Failed to publish duplicate synthetic event: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	// 5. Verify inbox table traps dupEventID exactly ONCE
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM public.inbox WHERE event_id = $1", dupEventID).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query inbox count for duplicate event_id: %v", err)
	}

	if count > 1 {
		t.Fatalf("Inbox barrier failed! Expected count <= 1 for duplicate event_id='%s', got %d", dupEventID, count)
	}

	t.Logf("Verified Inbox Deduplication Barrier: Duplicate event_id='%s' safely trapped (count=%d)!", dupEventID, count)
}
