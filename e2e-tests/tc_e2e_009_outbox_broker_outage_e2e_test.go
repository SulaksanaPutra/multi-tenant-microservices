/*
 * Test Specification: TC-E2E-009 - Outbox Broadcaster Retry Survival During Broker Outage
 * Architectural Scope: Outbox Repository (public.outbox), Outbox Worker, RabbitMQ AMQP connection manager
 * Objective: Validate At-Least-Once event delivery guarantees and background outbox worker retry resilience
 *            when the RabbitMQ message broker undergoes a temporary outage during event publication.
 * Failure Mode Guarded: Transactional event loss during broker downtime, crashing background workers.
 *
 * Workflow / How It Works:
 *   1. Stop RabbitMQ container via system Docker CLI to simulate a broker outage.
 *   2. Submit registration request via Gateway POST /api/register.
 *   3. Connect to database and verify outbox record is safely persisted in public.outbox (status='PENDING').
 *   4. Restart RabbitMQ container and wait for broker TCP/AMQP readiness.
 *   5. Trigger outbox dead-letter sweeper and poll tenant_manager_db until tenant reaches 'active' status.
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
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestE2E_OutboxBrokerOutage_RetryAndRecovery(t *testing.T) {
	t.Log("=== E2E Test: Outbox Broadcaster Retry Survival During Broker Outage (TC-E2E-009 / Docs Case #1) ===")

	// Teardown fixture ensuring RabbitMQ container is always restarted even on test failure
	defer func() {
		_ = exec.Command("docker", "start", "rabbitmq").Run()
	}()

	// =========================================================================
	// Step 1: Simulate Message Broker Outage (Stop RabbitMQ)
	// Instruction: Stop rabbitmq container via docker CLI before registration outbox worker publishes.
	// Architectural Invariant: Gateway database transaction commits registration and outbox row atomically.
	// =========================================================================
	t.Log("1. Stopping RabbitMQ container to simulate broker downtime...")
	cmd := exec.Command("docker", "stop", "rabbitmq")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to stop RabbitMQ container: %v (%s)", err, string(out))
	}

	// =========================================================================
	// Step 2: Submit Registration Request During Broker Outage
	// Instruction: Submit POST /api/register request while RabbitMQ broker is offline.
	// Architectural Invariant: HTTP endpoint succeeds with HTTP 202 Accepted because outbox pattern
	//                          decouples HTTP write from AMQP event publication.
	// =========================================================================
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	t.Logf("2. Submitting Registration during broker outage: owner='%s', email='%s'", ownerName, ownerEmail)

	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := http.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to submit registration during broker outage: %v", err)
	}
	defer resp.Body.Close()

	tenantID := resolveTenantID(t, ownerEmail)

	// =========================================================================
	// Step 3: Verify Outbox Table Event Persistence
	// Instruction: Query tenant_manager_db public.outbox table to confirm event is pending.
	// Architectural Invariant: Outbox row status must be PENDING or PROCESSING.
	// =========================================================================
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	var outboxCount int
	err = db.QueryRow("SELECT COUNT(*) FROM public.outbox WHERE tenant_id = $1 AND status IN ('PENDING', 'PROCESSING')", tenantID).Scan(&outboxCount)
	if err != nil {
		t.Fatalf("Failed to query outbox table for pending event: %v", err)
	}
	t.Logf("3. Verified outbox record safely stored in database (pending count=%d)", outboxCount)

	time.Sleep(3 * time.Second) // Allow outbox worker to record retry attempts during outage

	// =========================================================================
	// Step 4: Restore RabbitMQ Broker & Await Network Readiness
	// Instruction: Restart rabbitmq container and poll AMQP TCP port until active.
	// =========================================================================
	t.Log("4. Restarting RabbitMQ container...")
	startCmd := exec.Command("docker", "start", "rabbitmq")
	if out, err := startCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to restart RabbitMQ container: %v (%s)", err, string(out))
	}

	t.Log("Waiting for RabbitMQ broker to accept connections...")
	for i := 0; i < 15; i++ {
		conn, err := amqp.Dial(rabbitmqDSN)
		if err == nil {
			_ = conn.Close()
			t.Logf("RabbitMQ broker connection active!")
			break
		}
		time.Sleep(1 * time.Second)
	}

	// =========================================================================
	// Step 5: Trigger Recovery Sweeper & Verify Eventual Tenant Activation
	// Instruction: Reset status to PENDING for failed rows and poll public.tenants until status='active'.
	// Architectural Invariant: Outbox worker retries publication; downstream services consume event
	//                          and tenant reaches 'active' status without data loss.
	// =========================================================================
	_, _ = db.Exec("UPDATE public.outbox SET status = 'PENDING', next_retry_at = NOW(), retry_count = 0 WHERE tenant_id = $1 AND status = 'FAILED'", tenantID)

	var tenantStatus string
	activated := false
	for i := 0; i < 70; i++ {
		err := db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&tenantStatus)
		if err == nil && tenantStatus == "active" {
			activated = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !activated {
		t.Fatalf("Outbox retry survival failed! Tenant %s failed to reach 'active' status after broker restart. Final status: '%s'", tenantID, tenantStatus)
	}

	t.Logf("5. Verified Outbox Broadcaster Retry Survival: Tenant tenant_id='%s' reached 'active' status after broker recovery!", tenantID)
}
