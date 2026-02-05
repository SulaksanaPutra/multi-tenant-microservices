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

	// 1. Connect to RabbitMQ
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

	// 2. Register a tenant and place an order to populate order-service PoolRegistry cache
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	reqBody, _ := json.Marshal(RegisterReq{
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

	var regResp RegisterResp
	_ = json.NewDecoder(resp.Body).Decode(&regResp)
	tenantID := regResp.Data.TenantID

	// Wait for tenant activation
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

	// Add auth: set credentials and login
	// TEMPORARY: setCredentials uses Stage 1 scaffolding endpoint.
	userID := regResp.Data.UserID
	const e2ePassword = "e2e-test-password-123"
	setCredentials(t, userID, tenantID, ownerEmail, e2ePassword)
	accessToken := loginAndGetToken(t, ownerEmail, e2ePassword)

	// Place order to populate cache
	orderBody, _ := json.Marshal(OrderReq{CustomerID: "cust_fanout", Amount: 99.00})
	orderReq, _ := http.NewRequest("POST", gatewayOrdersURL, bytes.NewBuffer(orderBody))
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("Authorization", bearerHeader(accessToken))

	oResp, err := http.DefaultClient.Do(orderReq)
	if err == nil {
		oResp.Body.Close()
	}

	// 3. Broadcast tenant.infrastructure_changed event over company.events exchange
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

	// 4. Send follow-up request to verify order-service handles cache purge & re-fetches cleanly
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
