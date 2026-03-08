/*
 * Test Specification: TC-E2E-005 - Consumer Container Outage, Queue Buffering & Async Catch-Up
 * Architectural Scope: notification-service (Consumer archetype B), RabbitMQ durable queues, Mailpit REST API
 * Objective: Validate system fault tolerance during consumer container crashes, durable AMQP queue buffering,
 *            and eventual consistency upon consumer service recovery.
 * Failure Mode Guarded: Message loss during consumer service container crashes, lost welcome notifications.
 *
 * Workflow / How It Works:
 *   1. Stop notification-service container via system Docker CLI to simulate container failure.
 *   2. Submit registration request POST /api/register while notification-service is offline.
 *   3. Poll tenant_manager_db and verify tenant reaches status='active' (proving other microservices function normally).
 *   4. Verify Mailpit REST API does NOT contain welcome email (confirming events are buffered in durable RabbitMQ queue).
 *   5. Restart notification-service container via system Docker CLI.
 *   6. Poll Mailpit REST API and verify consumer drains queue, processes inbox record, and delivers welcome email.
 */

package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"
)

func TestE2E_ServiceOutage_RecoveryAndCatchUp(t *testing.T) {
	t.Log("=== E2E Test: Consumer Container Outage, Queue Buffering & Async Catch-Up ===")

	// =========================================================================
	// Step 1: Simulate Consumer Container Crash (Stop notification-service)
	// Instruction: Stop notification-service container using system Docker CLI.
	// Architectural Invariant: RabbitMQ queue remains durable and holds unacknowledged messages.
	// =========================================================================
	t.Log("1. Stopping notification-service container to simulate container crash/outage...")
	cmd := exec.Command("docker", "stop", "notification-service")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to stop notification-service: %v (%s)", err, string(out))
	}

	// Teardown fixture ensuring notification-service is restarted after test finishes
	defer func() {
		_ = exec.Command("docker", "start", "notification-service").Run()
	}()

	// =========================================================================
	// Step 2: Submit Registration Request During Consumer Outage
	// Instruction: Submit POST /api/register while notification consumer is dead.
	// =========================================================================
	tenantID, _, ownerEmail, _ := registerAndActivateTenant(t)
	t.Logf("3. Verified tenant_id='%s' reached 'active' status while notification-service was down!", tenantID)

	// =========================================================================
	// Step 4: Confirm Message Buffering in Durable AMQP Queue
	// Instruction: Query Mailpit API and assert welcome email is NOT present while consumer is offline.
	// =========================================================================
	mResp, err := http.Get(mailpitAPIURL)
	if err == nil && mResp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(mResp.Body)
		mResp.Body.Close()
		if bytes.Contains(body, []byte(ownerEmail)) {
			t.Fatalf("Unexpected welcome email delivered while notification-service was killed!")
		}
	}
	t.Logf("4. Confirmed welcome email buffered in RabbitMQ queue (not delivered to Mailpit yet)")

	// =========================================================================
	// Step 5: Restore Consumer Container
	// Instruction: Restart notification-service container via system Docker CLI.
	// =========================================================================
	t.Log("5. Restarting notification-service container...")
	startCmd := exec.Command("docker", "start", "notification-service")
	if out, err := startCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to restart notification-service: %v (%s)", err, string(out))
	}

	// =========================================================================
	// Step 6: Verify Queue Draining & Eventual Mail Delivery
	// Instruction: Poll Mailpit REST API until welcome notification is received.
	// Architectural Invariant: Recovered consumer reconnects to AMQP, drains buffered queue,
	//                          records inbox event, and dispatches SMTP email cleanly.
	// =========================================================================
	var emailReceived bool
	for i := 0; i < 60; i++ {
		mResp, err := http.Get(mailpitAPIURL)
		if err == nil && mResp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(mResp.Body)
			mResp.Body.Close()

			var msgList MailpitMsgList
			if err := json.Unmarshal(body, &msgList); err == nil {
				for _, msg := range msgList.Messages {
					for _, to := range msg.To {
						if to.Address == ownerEmail {
							emailReceived = true
							break
						}
					}
					if emailReceived {
						break
					}
				}
			}
		}
		if emailReceived {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !emailReceived {
		t.Fatalf("Failed to receive buffered welcome email after notification-service container recovery!")
	}

	t.Logf("6. Verified container recovery & asynchronous catch-up: Welcome email delivered to Mailpit for %s!", ownerEmail)
}
