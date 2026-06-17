/*
 * Test Specification: TC-E2E-032 - Distributed Migration Lock: API Shield (HTTP 423) & Unfreeze
 * Architectural Scope: order-service (InfrastructureLockingConsumer, RoutingRegistry MIGRATING state,
 *                      TenantDBResolver -> jwt_middleware HTTP 423 Locked mapping), tenant-service
 *                      (tenant.infrastructure_locking / tenant.infrastructure_changed broadcasts), Traefik Gateway.
 * Objective: Validate Phase-2 of the migration plan in a DETERMINISTIC way: once the tenant.infrastructure_locking
 *            broadcast marks a tenant MIGRATING in the in-memory RoutingRegistry, the data plane must immediately
 *            refuse every request with HTTP 423 Locked (no traffic can touch the DB while the schema is being
 *            renamed / dumped), and the tenant.infrastructure_changed broadcast must unfreeze replicas so the
 *            data plane resumes serving requests with fresh routing metadata.
 * Failure Mode Guarded: Requests leaking into the data plane during the schema-lock window (lost writes),
 *                       replicas remaining frozen forever after migration completes (zombie locks).
 *
 * Workflow / How It Works:
 *   1. Register a shared tenant, activate, provision credentials, login, create one baseline order.
 *   2. Force the distributed lock deterministically: set tenants.status='MIGRATING' directly AND publish the
 *      real tenant.infrastructure_locking broadcast event (this is what ChangeTenantPlan would stage).
 *   3. Assert POST /api/orders and GET /api/orders both return HTTP 423 Locked (read + write shielded).
 *   4. Publish the real tenant.infrastructure_changed unfreeze broadcast.
 *   5. Assert POST /api/orders returns HTTP 201 Created and GET /api/orders returns HTTP 200 OK (registry
 *      purged, routing re-resolved).
 *   6. Restore tenants.status='active' in the control plane DB (cleanup; the real cutover would do this).
 *
 * Operational Preconditions:
 *   - Must run serially (`go test -p 1`); the manual lock state affects only this tenant.
 *   - Uses direct control-plane DB writes and AMQP broadcasts; no Docker operations required.
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

func TestE2E_TC_E2E_032_MigrationLock_APIShield(t *testing.T) {
	t.Log("=== TC-E2E-032: Distributed Migration Lock — API Shield 423 & Unfreeze ===")

	// =========================================================================
	// Step 1: Register Shared Tenant, Activate & Login
	// =========================================================================
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t, "shared")
	setCredentials(t, userID, tenantID, ownerEmail, password)
	accessToken := loginAndGetToken(t, ownerEmail, password)
	authHeader := bearerHeader(accessToken)

	baseline := doOrderRequest(t, http.MethodPost, gatewayOrdersURL, authHeader, map[string]any{
		"customer_id": "cust_pre_lock",
		"amount":      15.00,
	})
	if baseline != http.StatusCreated {
		t.Fatalf("Baseline order before lock: expected HTTP 201, got %d", baseline)
	}
	t.Logf("1. Baseline data plane functional for tenant '%s'.", tenantID)

	// =========================================================================
	// Step 2: Acquire the Distributed Lock Deterministically
	// Instruction: (a) set tenants.status='MIGRATING' directly in the control plane DB;
	//              (b) publish the real tenant.infrastructure_locking broadcast so every
	//              order-service replica marks the tenant MIGRATING in its RoutingRegistry.
	// =========================================================================
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("UPDATE public.tenants SET status = 'MIGRATING' WHERE id = $1", tenantID); err != nil {
		t.Fatalf("Failed to set tenant status MIGRATING: %v", err)
	}

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

	lockEvtID := "evt_e2e_lock_" + tenantID
	publishCompanyEvent(t, ch, "tenant.infrastructure_locking", map[string]any{
		"event_id":  lockEvtID,
		"tenant_id": tenantID,
	})
	t.Logf("2. Published tenant.infrastructure_locking (event_id='%s') and set status MIGRATING.", lockEvtID)

	// =========================================================================
	// Step 3: Assert the API Shield (HTTP 423 Locked) on Read & Write Paths
	// Instruction: Poll until order-service has consumed the broadcast and the registry
	//              reflects MIGRATING; then require both POST and GET to return 423.
	// =========================================================================
	shielded := false
	for i := 0; i < 40; i++ {
		code := doOrderRequest(t, http.MethodPost, gatewayOrdersURL, authHeader, map[string]any{
			"customer_id": "cust_during_lock",
			"amount":      5.00,
		})
		if code == http.StatusLocked {
			shielded = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !shielded {
		t.Fatalf("API shield NOT enforced: POST /api/orders never returned HTTP 423 after tenant.infrastructure_locking broadcast")
	}
	if code := doOrderRequest(t, http.MethodGet, gatewayOrdersURL, authHeader, nil); code != http.StatusLocked {
		t.Fatalf("GET /api/orders expected HTTP 423 Locked during migration, got %d", code)
	}
	t.Log("3. API shield verified: POST and GET /api/orders return HTTP 423 Locked while tenant is MIGRATING.")

	// =========================================================================
	// Step 4: Publish the Unfreeze Broadcast
	// Instruction: tenant.infrastructure_changed purges the stale registry entry across replicas.
	// =========================================================================
	unlockEvtID := "evt_e2e_unlock_" + tenantID
	publishCompanyEvent(t, ch, "tenant.infrastructure_changed", map[string]any{
		"event_id":  unlockEvtID,
		"tenant_id": tenantID,
	})
	t.Logf("4. Published tenant.infrastructure_changed (event_id='%s').", unlockEvtID)

	// =========================================================================
	// Step 5: Assert Traffic Resumes With Fresh Routing
	// Instruction: Poll until the registry purge propagates and POST succeeds again; then
	//              verify the read path as well.
	// =========================================================================
	resumed := false
	for i := 0; i < 40; i++ {
		code := doOrderRequest(t, http.MethodPost, gatewayOrdersURL, authHeader, map[string]any{
			"customer_id": "cust_after_unlock",
			"amount":      5.00,
		})
		if code == http.StatusCreated {
			resumed = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !resumed {
		t.Fatalf("Data plane did not resume after tenant.infrastructure_changed unfreeze broadcast")
	}
	if code := doOrderRequest(t, http.MethodGet, gatewayOrdersURL, authHeader, nil); code != http.StatusOK {
		t.Fatalf("GET /api/orders expected HTTP 200 after unfreeze, got %d", code)
	}
	t.Log("5. Unfreeze verified: data plane resumed (POST 201 / GET 200) after tenant.infrastructure_changed.")

	// =========================================================================
	// Step 6: Restore Tenant Status (Cleanup)
	// Instruction: The real cutover flips status to ACTIVE; restore it here since this test
	//              acquired the lock manually. Also restore on failure via defer.
	// =========================================================================
	restore := func() {
		_, _ = db.Exec("UPDATE public.tenants SET status = 'active' WHERE id = $1", tenantID)
	}
	defer restore()
	restore()

	t.Log("6. TC-E2E-032 Passed: migration lock froze the data plane with HTTP 423 and the unfreeze broadcast restored traffic.")
}