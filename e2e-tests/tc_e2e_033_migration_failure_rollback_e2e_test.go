/*
 * Test Specification: TC-E2E-033 - Migration Failure Rollback Saga (tenant.migration_failed)
 * Architectural Scope: tenant-service (MigrationFailedConsumer, workspace_service.RollbackFailedMigration),
 *                      order-service (InfrastructureLockingConsumer, InfrastructureChangedConsumer, RoutingRegistry),
 *                      control plane DB (public.tenants), RabbitMQ company.events exchange.
 * Objective: Validate the compensating rollback branch of the migration plan (Phase 3 warning + the
 *            "Bulletproof" additions): when infra-provisioner cannot complete a migration it must publish
 *            tenant.migration_failed, and tenant-service must (a) flip the tenant status back to ACTIVE and
 *            (b) stage a tenant.infrastructure_changed unfreeze broadcast so order-service replicas purge the
 *            MIGRATING registry entry and resume traffic. The locked data plane must unfreeze end-to-end.
 * Failure Mode Guarded: Zombie MIGRATING tenants after a failed migration, replicas frozen forever on the
 *                       failure path (no compensating unfreeze), unrecoverable lock state without a cutover.
 *
 * Workflow / How It Works:
 *   1. Register a shared tenant, activate, provision credentials, login.
 *   2. Acquire the distributed lock deterministically (status MIGRATING + tenant.infrastructure_locking
 *      broadcast) and assert HTTP 423 on the data plane.
 *   3. Publish a synthetic tenant.migration_failed event (the exact payload infra-provisioner emits when
 *      its rollback saga triggers) for this tenant.
 *   4. Poll tenant_manager_db until status returns to 'active' (compensating action committed).
 *   5. Assert the tenant.infrastructure_changed unfreeze broadcast is received on an exclusive queue.
 *   6. Assert the data plane resumes: POST /api/orders -> 201, GET /api/orders -> 200.
 *
 * Operational Preconditions:
 *   - Must run serially (`go test -p 1`); only the target tenant's lock state is mutated.
 *   - Uses synthetic AMQP events + direct control-plane DB writes; no Docker operations required.
 */

package e2e_test

import (
	"database/sql"
	"net/http"
	"testing"
	"time"

	_ "github.com/lib/pq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestE2E_TC_E2E_033_MigrationFailed_RollbackSaga(t *testing.T) {
	t.Log("=== TC-E2E-033: Migration Failure Rollback Saga (tenant.migration_failed) ===")

	// =========================================================================
	// Step 1: Register Shared Tenant, Activate & Login
	// =========================================================================
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t, "shared")
	setCredentials(t, userID, tenantID, ownerEmail, password)
	accessToken := loginAndGetToken(t, ownerEmail, password)
	authHeader := bearerHeader(accessToken)
	t.Logf("1. Tenant '%s' active.", tenantID)

	// =========================================================================
	// Step 2: Bind Exclusive Queue to Observe the Unfreeze Broadcast
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
		t.Fatalf("Failed to declare exclusive queue: %v", err)
	}
	if err := ch.QueueBind(q.Name, "tenant.infrastructure_changed", "company.events", false, nil); err != nil {
		t.Fatalf("Failed to bind infra-changed broadcast: %v", err)
	}
	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("Failed to consume from broadcast queue: %v", err)
	}

	// =========================================================================
	// Step 3: Acquire the Distributed Lock (Simulated Migrating State)
	// Instruction: Set tenants.status='MIGRATING' and publish the real locking broadcast,
	//              then verify the data plane is shielded with HTTP 423.
	// =========================================================================
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	// Cleanup guard: if the test fails before the rollback consumer completes, restore ACTIVE.
	defer func() {
		_, _ = db.Exec("UPDATE public.tenants SET status = 'active' WHERE id = $1 AND status = 'MIGRATING'", tenantID)
	}()

	if _, err := db.Exec("UPDATE public.tenants SET status = 'MIGRATING' WHERE id = $1", tenantID); err != nil {
		t.Fatalf("Failed to set tenant status MIGRATING: %v", err)
	}
	publishCompanyEvent(t, ch, "tenant.infrastructure_locking", map[string]any{
		"event_id":  "evt_e2e_rollback_lock_" + tenantID,
		"tenant_id": tenantID,
	})

	shielded := false
	for i := 0; i < 40; i++ {
		if code := doOrderRequest(t, http.MethodPost, gatewayOrdersURL, authHeader, map[string]any{
			"customer_id": "cust_pre_rollback",
			"amount":      9.99,
		}); code == http.StatusLocked {
			shielded = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !shielded {
		t.Fatalf("Pre-rollback shield NOT enforced: expected HTTP 423 before publishing tenant.migration_failed")
	}
	t.Log("3. Distributed lock acquired; data plane returns HTTP 423.")

	// =========================================================================
	// Step 4: Publish tenant.migration_failed (Compensating Event)
	// Instruction: Simulate the infra-provisioner rollback saga payload. The tenant-service
	//              MigrationFailedConsumer must claim it (inbox-guarded) and roll back.
	// =========================================================================
	failEvtID := "evt_e2e_migration_failed_" + tenantID
	publishCompanyEvent(t, ch, "tenant.migration_failed", map[string]any{
		"event_id":  failEvtID,
		"tenant_id": tenantID,
		"reason":    "e2e forced rollback: ALTER SCHEMA lock timeout",
	})
	t.Logf("4. Published tenant.migration_failed (event_id='%s').", failEvtID)

	// =========================================================================
	// Step 5: Assert the Compensating Rollback (status back to ACTIVE)
	// Instruction: Poll tenant_manager_db until the MigrationFailedConsumer commits the
	//              RollbackFailedMigration unit-of-work.
	// =========================================================================
	rolledBack := false
	for i := 0; i < 60; i++ { // up to 30s at 500ms
		if status := currentTenantStatus(t, db, tenantID); status == "active" {
			rolledBack = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !rolledBack {
		t.Fatalf("Rollback saga failed: tenant '%s' did not return to ACTIVE after tenant.migration_failed (final: '%s')",
			tenantID, currentTenantStatus(t, db, tenantID))
	}
	t.Logf("5. Compensating rollback committed: tenant '%s' status restored to ACTIVE.", tenantID)

	// =========================================================================
	// Step 6: Assert the Unfreeze Broadcast (tenant.infrastructure_changed)
	// Instruction: RollbackFailedMigration stages the infra-changed broadcast; order-service
	//              replicas must consume it to purge the MIGRATING registry entry.
	// =========================================================================
	waitForCompanyEvent(t, msgs, "tenant.infrastructure_changed", tenantID, 20*time.Second)
	t.Log("6. Received tenant.infrastructure_changed unfreeze broadcast after rollback.")

	// =========================================================================
	// Step 7: Assert the Data Plane Unfroze
	// Instruction: After the registry purge, the next request re-resolves routing metadata
	//              and the data plane must accept writes and reads again.
	// =========================================================================
	resumed := false
	for i := 0; i < 40; i++ {
		if code := doOrderRequest(t, http.MethodPost, gatewayOrdersURL, authHeader, map[string]any{
			"customer_id": "cust_post_rollback",
			"amount":      21.00,
		}); code == http.StatusCreated {
			resumed = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !resumed {
		t.Fatalf("Data plane did not unfreeze after the rollback unfreeze broadcast")
	}
	if code := doOrderRequest(t, http.MethodGet, gatewayOrdersURL, authHeader, nil); code != http.StatusOK {
		t.Fatalf("GET /api/orders expected HTTP 200 after rollback, got %d", code)
	}

	t.Log("7. TC-E2E-033 Passed: migration failure rollback restored ACTIVE status, broadcast the unfreeze, and the data plane resumed traffic.")
}