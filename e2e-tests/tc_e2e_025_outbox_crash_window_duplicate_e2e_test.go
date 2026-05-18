/*
 * Test Specification: TC-E2E-025 - Sender-Side Outbox Crash-Window Duplicate Republish (Docs Case #3, Full Loop)
 * Architectural Scope: Outbox Repository (public.outbox), Outbox Worker (FOR UPDATE SKIP LOCKED sweeper),
 *                      Inbox Repository (public.inbox), notification-service (WorkspaceReadyConsumer),
 *                      Mailpit REST API.
 * Objective: Validate the COMPLETE outbox+inbox closed loop for the "phantom batch" crash window: a message is
 *            published to RabbitMQ, the sender crashes before marking it PUBLISHED, and the outbox row is reset
 *            to PENDING (the state RecoverStuckClaims would produce for a stuck PROCESSING claim) so the outbox
 *            worker republishes the identical event. The downstream inbox barrier must trap the duplicate so
 *            exactly ONE inbox record and exactly ONE welcome email exist.
 * Failure Mode Guarded: Duplicate domain side-effects after the publish-then-crash-then-republish sequence,
 *                       duplicate welcome emails under sender-side at-least-once delivery.
 *
 * Workflow / How It Works:
 *   1. Register a shared-plan tenant and await activation (baseline welcome email is dispatched once).
 *   2. Resolve the tenant's `workspace.ready` outbox row (PUBLISHED) whose row ID doubles as the inbox event_id.
 *   3. Assert baseline: public.inbox contains exactly 1 row for that event_id, and Mailpit has exactly 1 email.
 *   4. Simulate the sender crash window: force the outbox row back to PENDING (status='PENDING', claimed_at=NULL),
 *      reproducing the post-crash state that RecoverStuckClaims would produce (row eligible for re-claim).
 *   5. Wait for the outbox worker to claim and republish the row (status returns to PUBLISHED).
 *   6. Assert idempotency: inbox row count is STILL exactly 1 and Mailpit STILL has exactly 1 welcome email.
 */

package e2e_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestE2E_TC_E2E_025_OutboxCrashWindow_DuplicateRepublish(t *testing.T) {
	t.Log("=== TC-E2E-025: Sender-Side Outbox Crash-Window Duplicate Republish (Docs Case #3) ===")

	// =========================================================================
	// Step 1: Register Shared Tenant & Await Activation
	// Instruction: Register tenant via Gateway and poll tenant_manager_db until 'active'.
	// =========================================================================
	tenantID, _, ownerEmail, _ := registerAndActivateTenant(t)
	t.Logf("2. Tenant '%s' is active.", tenantID)

	// =========================================================================
	// Step 2: Resolve workspace.ready Outbox Row (PUBLISHED)
	// Instruction: Query public.outbox for the tenant's workspace.ready event and wait
	//              until the outbox worker has marked it PUBLISHED.
	// Architectural Invariant: The outbox row ID is carried as event_id in the published
	//                          payload and stored as event_id in the consumer inbox table.
	// =========================================================================
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	notifDB, err := sql.Open("postgres", notificationDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to notification_db: %v", err)
	}
	defer notifDB.Close()

	var outboxID, outboxStatus string
	readyRowPublished := false
	for i := 0; i < 30; i++ {
		err = db.QueryRow(
			"SELECT id, status FROM public.outbox WHERE tenant_id = $1 AND event_type = 'workspace.ready' ORDER BY created_at DESC LIMIT 1",
			tenantID,
		).Scan(&outboxID, &outboxStatus)
		if err == nil && outboxStatus == "PUBLISHED" {
			readyRowPublished = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !readyRowPublished {
		t.Fatalf("workspace.ready outbox row for tenant '%s' did not reach PUBLISHED status (last err: %v)", tenantID, err)
	}
	t.Logf("3. Resolved PUBLISHED workspace.ready outbox row id='%s' (event_id).", outboxID)

	// =========================================================================
	// Step 3: Assert Baseline Idempotency (single inbox row + single email)
	// Instruction: Confirm exactly one inbox record exists for the event_id and exactly
	//              one welcome email was delivered before the crash-window simulation.
	// =========================================================================
	baselineInboxCount, baselineEmailCount := settleBaselineCounts(t, notifDB, outboxID, ownerEmail)
	if baselineInboxCount != 1 {
		t.Fatalf("Baseline violation: expected exactly 1 inbox row for event_id='%s', got %d", outboxID, baselineInboxCount)
	}
	if baselineEmailCount != 1 {
		t.Fatalf("Baseline violation: expected exactly 1 welcome email for '%s', got %d", ownerEmail, baselineEmailCount)
	}
	t.Logf("4. Baseline confirmed: inbox rows=%d, welcome emails=%d.", baselineInboxCount, baselineEmailCount)

	// =========================================================================
	// Step 4: Simulate Sender Crash Window (sweeper-style reset to PENDING)
	// Instruction: Reset the PUBLISHED outbox row back to PENDING with cleared claim
	//              timestamps. NOTE: production RecoverStuckClaims only resets rows
	//              stuck in PROCESSING for >30s; we shortcut the equivalent post-crash
	//              state (row eligible for re-claim + republish) by resetting PUBLISHED
	//              directly to PENDING so the at-least-once republish path is exercised.
	// =========================================================================
	res, err := db.Exec(
		"UPDATE public.outbox SET status = 'PENDING', claimed_at = NULL, processed_at = NULL WHERE id = $1 AND status = 'PUBLISHED'",
		outboxID,
	)
	if err != nil {
		t.Fatalf("Failed to force outbox row '%s' back to PENDING: %v", outboxID, err)
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected != 1 {
		t.Fatalf("Crash-window simulation failed: expected 1 outbox row reset to PENDING, got %d", rowsAffected)
	}
	t.Logf("5. Simulated sender crash window: outbox row '%s' forced back to PENDING.", outboxID)

	// =========================================================================
	// Step 5: Wait for Outbox Worker to Republish the Duplicate Event
	// Instruction: Poll until the outbox worker claims, republishes, and marks the row
	//              PUBLISHED again (proving at-least-once republish occurred).
	// =========================================================================
	republished := false
	for i := 0; i < 40; i++ {
		err = db.QueryRow("SELECT status FROM public.outbox WHERE id = $1", outboxID).Scan(&outboxStatus)
		if err == nil && outboxStatus == "PUBLISHED" {
			republished = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !republished {
		t.Fatalf("Outbox worker failed to republish event_id='%s' after crash-window reset (status='%s')", outboxID, outboxStatus)
	}
	t.Logf("6. Outbox worker republished the duplicate event_id='%s'.", outboxID)

	// =========================================================================
	// Step 6: Assert Crash-Window Idempotency (no duplicate side-effects)
	// Instruction: Poll until the downstream consumer has processed the republished
	//              duplicate through the inbox barrier, then assert counts.
	// Architectural Invariant: ON CONFLICT (event_id) DO NOTHING traps the republished
	//                          duplicate; exactly one inbox row and one welcome email remain.
	// =========================================================================
	var finalInboxCount, finalEmailCount int
	settled := false
	for i := 0; i < 40; i++ {
		finalInboxCount = countInboxRows(t, notifDB, outboxID)
		finalEmailCount = countMailpitEmails(t, ownerEmail)
		if finalInboxCount == 1 && finalEmailCount == 1 {
			settled = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !settled {
		t.Fatalf("Crash-window dedup FAILED: expected exactly 1 inbox row for event_id='%s' and 1 welcome email for '%s', got inbox=%d, emails=%d",
			outboxID, ownerEmail, finalInboxCount, finalEmailCount)
	}

	t.Logf("7. TC-E2E-025 Passed: Duplicate republish after sender crash-window safely trapped (inbox=%d, emails=%d).", finalInboxCount, finalEmailCount)
}

// countInboxRows returns the number of inbox records matching the given event_id.
func countInboxRows(t *testing.T, db *sql.DB, eventID string) int {
	t.Helper()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM public.inbox WHERE event_id = $1", eventID).Scan(&count); err != nil {
		t.Fatalf("Failed to query inbox count for event_id='%s': %v", eventID, err)
	}
	return count
}

// settleBaselineCounts polls until the notification consumer has finished its
// post-commit SMTP dispatch phase for the baseline event, then returns the
// settled inbox row count and welcome email count for the given event/recipient.
func settleBaselineCounts(t *testing.T, notifDB *sql.DB, eventID, ownerEmail string) (inboxCount, emailCount int) {
	t.Helper()

	for i := 0; i < 30; i++ {
		inboxCount = countInboxRows(t, notifDB, eventID)
		emailCount = countMailpitEmails(t, ownerEmail)
		if inboxCount >= 1 && emailCount >= 1 {
			return inboxCount, emailCount
		}
		time.Sleep(500 * time.Millisecond)
	}
	return inboxCount, emailCount
}

// countMailpitEmails returns the number of Mailpit messages addressed to the given email.
func countMailpitEmails(t *testing.T, address string) int {
	t.Helper()

	resp, err := http.Get(mailpitAPIURL)
	if err != nil {
		t.Fatalf("Failed to query Mailpit API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Mailpit API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read Mailpit response: %v", err)
	}

	var msgList MailpitMsgList
	if err := json.Unmarshal(body, &msgList); err != nil {
		t.Fatalf("Failed to unmarshal Mailpit response: %v", err)
	}

	count := 0
	for _, msg := range msgList.Messages {
		for _, to := range msg.To {
			if to.Address == address {
				count++
				break
			}
		}
	}
	return count
}
