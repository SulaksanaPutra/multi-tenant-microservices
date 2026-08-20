/*
 * Test Specification: TC-E2E-030 - Real Plan Upgrade: Distributed Lock, Data Migration & Cutover Contract
 * Architectural Scope: tenant-service (ChangeTenantPlanMe, workspace_service.ChangeTenantPlan), order-service
 *                      (InfrastructureLockingConsumer / RoutingRegistry, TenantDBResolver 423 shield),
 *                      infra-provisioner (WorkspaceInitiatedConsumer, DockerProvisioner, SchemaMigrator),
 *                      tenant-service (TenantOrderDBReadyConsumer -> TenantInfrastructureService), Traefik Gateway.
 * Objective: Validate the REAL plan-upgrade migration contract (previously proposed but unimplemented in §5.1).
 *            A shared-plan tenant's plan is switched to dedicated; the tenant flips to MIGRATING, the
 *            tenant.infrastructure_locking broadcast freezes order-service replicas (HTTP 423 Locked), a dedicated
 *            container is provisioned, and the tenant order_db.ready cutover flips the tenant back to ACTIVE and
 *            broadcasts tenant.infrastructure_changed so traffic resumes and routes to the dedicated container.
 * Failure Mode Guarded: Schema-lock "lost write" window (data plane serving requests after the shared schema is
 *                       renamed), zombie MIGRATING tenants after cutover, stale routing to shared_db post-migration.
 *
 * Workflow / How It Works:
 *   1. Register a shared-plan tenant, await activation, provision credentials, login, seed one order.
 *   2. Bind an exclusive anonymous queue to company.events for the locking + infra-changed broadcasts.
 *   3. PUT /api/tenants/me/plan {"plan":"dedicated"}; tenant status must transition to MIGRATING in DB.
 *   4. Assert the tenant.infrastructure_locking broadcast is received (fanout to all replicas).
 *   5. During the MIGRATING window issue order requests; every in-window failure must be HTTP 423 Locked and at
 *      least one 423 must be observed before cutover (the 423 pick-up is timing dependent on the provisioning
 *      pipeline, hence the bounded retry loop). Any 5xx during the window fails the "no lost writes" invariant.
 *   6. Poll tenant_manager_db until status returns to ACTIVE (dedicated container provisioned + goose migrations
 *      + tenant.order_db.ready cutover), and assert the tenant.infrastructure_changed broadcast is received.
 *   7. Assert GET /api/tenants/me now returns status ACTIVE + plan dedicated (frontend short-poll contract).
 *   8. Issue POST + GET /api/orders asserting the data plane is functional against the new dedicated routing.
 *
 * Operational Preconditions:
 *   - Requires the infra-provisioner container to have access to the host Docker socket (provisions the
 *     dedicated postgres-tenant-<id> container with 512MB RAM / 0.5 CPU) — same requirement as TC-E2E-002.
 *   - The postgres:16-alpine image must be pullable/buildable by infra-provisioner at runtime.
 *   - Must run serially (`go test -p 1`); the plan upgrade mutates shared broker/container state.
 *
 * Known Environment Caveat:
 *   The infra-provisioner runtime image is postgres:16-alpine (pg_dump/psql/sed present), so the
 *   MigrateData pipeline actually copies rows between the shared schema and the dedicated container.
 *   This test therefore additionally asserts pre-upgrade data survival after cutover, exercises the
 *   dedicated -> shared downgrade path asserting the data is migrated back, and asserts the dedicated
 *   container is purged after the downgrade.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestE2E_TC_E2E_030_PlanUpgrade_MigrationContract(t *testing.T) {
	t.Log("=== TC-E2E-030: Real Plan Upgrade — Distributed Lock, Data Migration & Cutover Contract ===")

	// =========================================================================
	// Step 1: Register Shared Tenant, Activate & Seed an Order
	// Instruction: Reuse the standard shared-plan registration/activation helper.
	// =========================================================================
	tenantID, userID, ownerEmail, password := registerAndActivateTenant(t, "shared")
	setCredentials(t, userID, tenantID, ownerEmail, password)
	accessToken := loginAndGetToken(t, ownerEmail, password)
	authHeader := bearerHeader(accessToken)

	seedCode := doOrderRequest(t, http.MethodPost, gatewayOrdersURL, authHeader, map[string]any{
		"customer_id": "cust_pre_migration",
		"quantity":    1,
		"price":       125.50,
		"currency":    "USD",
	})
	if seedCode != http.StatusCreated {
		t.Fatalf("Seed order before plan upgrade: expected HTTP 201 Created, got %d", seedCode)
	}
	t.Logf("1. Shared tenant '%s' active with a seeded order (customer_id='cust_pre_migration').", tenantID)

	// =========================================================================
	// Step 2: Bind Exclusive Queue for the Migration Broadcasts
	// Instruction: Bind an anonymous exclusive queue to company.events on the
	//              tenant.infrastructure_locking and tenant.infrastructure_changed keys.
	// Architectural Invariant: Broadcast events reach ALL replicas via the shared routing key.
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
	if err := ch.QueueBind(q.Name, "tenant.infrastructure_locking", "company.events", false, nil); err != nil {
		t.Fatalf("Failed to bind locking broadcast: %v", err)
	}
	if err := ch.QueueBind(q.Name, "infrastructure.provisioned", "company.events", false, nil); err != nil {
		t.Fatalf("Failed to bind provisioned broadcast: %v", err)
	}
	if err := ch.QueueBind(q.Name, "tenant.infrastructure_changed", "company.events", false, nil); err != nil {
		t.Fatalf("Failed to bind infra-changed broadcast: %v", err)
	}
	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("Failed to consume from broadcast queue: %v", err)
	}
	t.Log("2. Bound exclusive queue to migration broadcast routing keys.")

	// =========================================================================
	// Step 3: Trigger Real Plan Upgrade (shared -> dedicated)
	// Instruction: PUT /api/tenants/me/plan with plan='dedicated'. The workspace
	//              service sets status MIGRATING and stages tenant.infrastructure_locking
	//              + workspace.initiated outbox events.
	// =========================================================================
	planBody, _ := json.Marshal(map[string]string{"plan": "dedicated"})
	req, _ := http.NewRequest(http.MethodPut, gatewayBaseURL+"/api/tenants/me/plan", bytes.NewBuffer(planBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/tenants/me/plan failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/tenants/me/plan expected HTTP 200, got %d", resp.StatusCode)
	}

	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	// Poll DB until the tenant enters MIGRATING (written synchronously inside ChangeTenantPlan).
	migrating := false
	for i := 0; i < 30; i++ {
		if status := currentTenantStatus(t, db, tenantID); status == "MIGRATING" {
			migrating = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !migrating {
		t.Fatalf("Tenant '%s' did not transition to MIGRATING after plan change", tenantID)
	}
	t.Logf("3. Plan change accepted; tenant '%s' status=MIGRATING.", tenantID)

	// =========================================================================
	// Step 4: Assert tenant.infrastructure_locking Broadcast
	// Instruction: The broadcast event must arrive at the exclusive fanout queue with
	//              the correct tenant_id before infra-provisioner begins mutating the schema.
	// =========================================================================
	waitForCompanyEvent(t, msgs, "tenant.infrastructure_locking", tenantID, 15*time.Second)
	t.Logf("4. Received tenant.infrastructure_locking broadcast for tenant '%s'.", tenantID)

	// =========================================================================
	// Step 5: API Shield Invariant During the MIGRATING Window
	// Instruction: While status == MIGRATING, every request must either succeed (201,
	//              shield not yet propagated in order-service) or be rejected with HTTP 423
	//              Locked. Any other code (especially 5xx from a renamed schema) violates the
	//              no-lost-writes contract. At least one 423 must be observed before cutover.
	// =========================================================================
	lockObserved := false
	for i := 0; i < 80; i++ {
		code := doOrderRequest(t, http.MethodPost, gatewayOrdersURL, authHeader, map[string]any{
			"customer_id": "cust_during_migration",
			"quantity":    1,
			"price":       10.00,
			"currency":    "USD",
		})
		if code == http.StatusLocked {
			lockObserved = true
			break
		}
		if code != http.StatusCreated {
			t.Fatalf("API shield violation during MIGRATING window: expected HTTP 201 or 423, got %d", code)
		}
		if status := currentTenantStatus(t, db, tenantID); status == "active" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !lockObserved {
		t.Fatalf("Migration shield NOT observed: tenant '%s' left the MIGRATING window without returning HTTP 423. "+
			"Verify infra-provisioner has Docker socket access (see operational preconditions).", tenantID)
	}
	t.Log("5. Data-plane API shield enforced: requests during MIGRATING rejected with HTTP 423 Locked.")

	// =========================================================================
	// Step 6: Await Cutover (dedicated container provisioned + activated)
	// Instruction: Poll tenant_manager_db until status returns to ACTIVE. This covers
	//              delivery of tenant.order_db.ready and the infra routing upsert.
	// =========================================================================
	activated := false
	for i := 0; i < 360; i++ { // up to 3 minutes at 500ms
		if status := currentTenantStatus(t, db, tenantID); status == "active" {
			activated = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !activated {
		t.Fatalf("Cutover failed: tenant '%s' did not return to ACTIVE within 3 minutes (final: '%s')",
			tenantID, currentTenantStatus(t, db, tenantID))
	}
	t.Logf("6. Cutover complete: tenant '%s' returned to ACTIVE.", tenantID)

	// =========================================================================
	// Step 6.5: Assert the infrastructure.provisioned Broadcast (Success Discriminator)
	// Instruction: On the SUCCESS path infra-provisioner publishes infrastructure.provisioned before
	//              order-service bootstraps the dedicated DB. On the failure path it publishes
	//              tenant.migration_failed instead and destroys the container — asserting this event
	//              proves the migration pipeline (not the rollback) produced the ACTIVE status.
	// =========================================================================
	waitForCompanyEvent(t, msgs, "infrastructure.provisioned", tenantID, 30*time.Second)
	t.Logf("6.5 Received infrastructure.provisioned broadcast for tenant '%s' (success path confirmed).", tenantID)

	// =========================================================================
	// Step 6.6: Assert the Dedicated Target Is Live
	// Instruction: container mode provisions postgres-tenant-<id>; same_instance
	//              (lite tier) creates a {tenant}_order_db database inside the
	//              shared postgres container. Assert the pre-upgrade order lives
	//              in whichever dedicated target was provisioned.
	// =========================================================================
	sanitizedTenantID := strings.ReplaceAll(strings.ToLower(tenantID), "-", "_")
	if isSameInstanceMode() {
		dbName := sanitizedTenantID + "_order_db"
		out, err := exec.Command("docker", "exec", "postgres", "psql", "-U", "postgres", "-d", "postgres",
			"-tAc", "SELECT 1 FROM pg_database WHERE datname='"+dbName+"'").CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "1" {
			t.Fatalf("same-instance tenant database '%s' not created after plan upgrade (out='%s', err=%v)", dbName, string(out), err)
		}
		ordOut, err := exec.Command("docker", "exec", "postgres", "psql", "-U", "postgres", "-d", dbName,
			"-tAc", "SELECT count(*) FROM public.orders WHERE customer_id='cust_pre_migration'").CombinedOutput()
		if err != nil || strings.TrimSpace(string(ordOut)) != "1" {
			t.Fatalf("same-instance tenant database '%s' missing the pre-upgrade order (out='%s', err=%v)", dbName, string(ordOut), err)
		}
		t.Logf("6.6 Same-instance tenant database '%s' holds the pre-upgrade order.", dbName)
	} else {
		containerName := "postgres-tenant-" + sanitizedTenantID
		inspect := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName)
		out, err := inspect.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "true" {
			t.Fatalf("Dedicated container '%s' not running after plan upgrade (out='%s', err=%v) — migration pipeline did not complete", containerName, string(out), err)
		}
		t.Logf("6.6 Dedicated container '%s' is running (512MB RAM / 0.5 CPU).", containerName)
	}

	// =========================================================================
	// Step 7: Assert tenant.infrastructure_changed Unfreeze Broadcast
	// Instruction: The activation stages an infrastructure_changed broadcast that purges
	//              the stale RoutingRegistry entry across all order-service replicas.
	// =========================================================================
	waitForCompanyEvent(t, msgs, "tenant.infrastructure_changed", tenantID, 20*time.Second)
	t.Logf("7. Received tenant.infrastructure_changed unfreeze broadcast for tenant '%s'.", tenantID)

	// =========================================================================
	// Step 8: Frontend Short-Poll Contract (GET /api/tenants/me)
	// Instruction: The plan requires the UI to short-poll this endpoint until status=ACTIVE.
	// =========================================================================
	getMe, _ := http.NewRequest(http.MethodGet, gatewayBaseURL+"/api/tenants/me", nil)
	getMe.Header.Set("Authorization", authHeader)
	meResp, err := defaultHTTPClient.Do(getMe)
	if err != nil {
		t.Fatalf("GET /api/tenants/me failed: %v", err)
	}
	defer meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/tenants/me expected HTTP 200, got %d", meResp.StatusCode)
	}
	var meBody struct {
		Data struct {
			Status string `json:"status"`
			Plan   string `json:"plan"`
		} `json:"data"`
	}
	if err := json.NewDecoder(meResp.Body).Decode(&meBody); err != nil {
		t.Fatalf("Failed to decode GET /api/tenants/me response: %v", err)
	}
	if meBody.Data.Status != "active" {
		t.Fatalf("Short-poll contract broken: expected tenant status 'active', got '%s'", meBody.Data.Status)
	}
	if meBody.Data.Plan != "dedicated" {
		t.Fatalf("Plan upgrade not persisted: expected plan 'dedicated', got '%s'", meBody.Data.Plan)
	}
	t.Logf("8. Short-poll contract verified: GET /api/tenants/me returns status='active', plan='dedicated'.")

	// =========================================================================
	// Step 9: Post-Cutover Data-Plane Functionality
	// Instruction: After the routing registry purge, order-service must re-resolve the
	//              dedicated container DSN and serve both writes and reads.
	// =========================================================================
	postCode := doOrderRequest(t, http.MethodPost, gatewayOrdersURL, authHeader, map[string]any{
		"customer_id": "cust_post_cutover",
		"quantity":    1,
		"price":       42.00,
		"currency":    "USD",
	})
	if postCode != http.StatusCreated {
		t.Fatalf("POST /api/orders after cutover: expected HTTP 201, got %d", postCode)
	}
	getCode := doOrderRequest(t, http.MethodGet, gatewayOrdersURL, authHeader, nil)
	if getCode != http.StatusOK {
		t.Fatalf("GET /api/orders after cutover: expected HTTP 200, got %d", getCode)
	}
	t.Logf("9. TC-E2E-030 Passed: plan upgrade completed the full lock -> migrate -> cutover contract; data plane served post-cutover (plan='dedicated').")

	// =========================================================================
	// Step 9.5: Pre-Upgrade Data Survival (the migration pipeline must copy rows)
	// Instruction: The order seeded before the plan upgrade must be queryable from
	//              the new dedicated routing; otherwise the migration was a no-op.
	// =========================================================================
	customersAfterUpgrade := listOrderCustomerIDs(t, authHeader)
	if !containsString(customersAfterUpgrade, "cust_pre_migration") {
		t.Fatalf("Data migration failed: pre-upgrade order 'cust_pre_migration' not found after cutover to dedicated. Orders: %v", customersAfterUpgrade)
	}
	if !containsString(customersAfterUpgrade, "cust_post_cutover") {
		t.Fatalf("Post-cutover order 'cust_post_cutover' not found after upgrade. Orders: %v", customersAfterUpgrade)
	}
	t.Logf("9.5 Pre-upgrade data survived cutover: orders %v visible on dedicated plan.", customersAfterUpgrade)

	// =========================================================================
	// Step 10: Downgrade (dedicated -> shared) — data must migrate back
	// Instruction: PUT plan='shared'; the saga runs in reverse and the tenant's
	//              orders must be migrated from the dedicated container back to the
	//              shared schema and remain queryable.
	// =========================================================================
	downBody, _ := json.Marshal(map[string]string{"plan": "shared"})
	downReq, _ := http.NewRequest(http.MethodPut, gatewayBaseURL+"/api/tenants/me/plan", bytes.NewBuffer(downBody))
	downReq.Header.Set("Content-Type", "application/json")
	downReq.Header.Set("Authorization", authHeader)
	downResp, err := defaultHTTPClient.Do(downReq)
	if err != nil {
		t.Fatalf("PUT /api/tenants/me/plan (downgrade) failed: %v", err)
	}
	defer downResp.Body.Close()
	if downResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/tenants/me/plan (downgrade) expected HTTP 200, got %d", downResp.StatusCode)
	}
	activatedDown := false
	for i := 0; i < 360; i++ {
		if status := currentTenantStatus(t, db, tenantID); status == "active" {
			activatedDown = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !activatedDown {
		t.Fatalf("Downgrade failed: tenant '%s' did not return to ACTIVE on shared plan (final: '%s')",
			tenantID, currentTenantStatus(t, db, tenantID))
	}
	downMe, _ := http.NewRequest(http.MethodGet, gatewayBaseURL+"/api/tenants/me", nil)
	downMe.Header.Set("Authorization", authHeader)
	downMeResp, err := defaultHTTPClient.Do(downMe)
	if err != nil {
		t.Fatalf("GET /api/tenants/me (downgrade) failed: %v", err)
	}
	defer downMeResp.Body.Close()
	var downMeBody struct {
		Data struct {
			Status string `json:"status"`
			Plan   string `json:"plan"`
		} `json:"data"`
	}
	if err := json.NewDecoder(downMeResp.Body).Decode(&downMeBody); err != nil {
		t.Fatalf("Failed to decode GET /api/tenants/me (downgrade) response: %v", err)
	}
	if downMeBody.Data.Plan != "shared" {
		t.Fatalf("Downgrade not persisted: expected plan 'shared', got '%s'", downMeBody.Data.Plan)
	}
	customersAfterDowngrade := listOrderCustomerIDs(t, authHeader)
	if !containsString(customersAfterDowngrade, "cust_pre_migration") {
		t.Fatalf("Downgrade data migration failed: pre-upgrade order 'cust_pre_migration' missing after downgrade to shared. Orders: %v", customersAfterDowngrade)
	}
	if !containsString(customersAfterDowngrade, "cust_post_cutover") {
		t.Fatalf("Downgrade data migration failed: post-upgrade order 'cust_post_cutover' missing after downgrade to shared. Orders: %v", customersAfterDowngrade)
	}

	// The dedicated resource must be released once the tenant is back on shared:
	// container mode purges the container; same_instance drops the tenant database.
	if isSameInstanceMode() {
		dbName := sanitizedTenantID + "_order_db"
		out, err := exec.Command("docker", "exec", "postgres", "psql", "-U", "postgres", "-d", "postgres",
			"-tAc", "SELECT 1 FROM pg_database WHERE datname='"+dbName+"'").CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			t.Fatalf("same-instance tenant database '%s' still exists after downgrade to shared", dbName)
		}
		ordOut, err := exec.Command("docker", "exec", "postgres", "psql", "-U", "postgres", "-d", "shared_db",
			"-tAc", "SELECT count(*) FROM "+dbName+".orders").CombinedOutput()
		if err != nil || strings.TrimSpace(string(ordOut)) == "0" {
			t.Fatalf("pre-upgrade order missing from shared schema after downgrade (out='%s', err=%v)", string(ordOut), err)
		}
		t.Logf("10. Downgrade passed (same_instance): tenant database '%s' dropped, orders %v back in shared schema.", dbName, customersAfterDowngrade)
	} else {
		containerName := "postgres-tenant-" + sanitizedTenantID
		afterOut, afterErr := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", containerName).CombinedOutput()
		if afterErr == nil && strings.TrimSpace(string(afterOut)) == "true" {
			t.Fatalf("Dedicated container '%s' still running after downgrade to shared — expected it to be purged", containerName)
		}
		t.Logf("10. Downgrade passed: tenant back on shared plan with orders %v preserved and dedicated container '%s' purged.", customersAfterDowngrade, containerName)
	}
}

func isSameInstanceMode() bool {
	tier := os.Getenv("TIER")
	if tier == "" {
		if data, err := os.ReadFile("../.active-tier"); err == nil {
			tier = strings.TrimSpace(string(data))
		}
	}
	return tier == "lite"
}

// listOrderCustomerIDs GETs the tenant's orders and returns the customer_id values.
func listOrderCustomerIDs(t *testing.T, authHeader string) []string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, gatewayOrdersURL, nil)
	req.Header.Set("Authorization", authHeader)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/orders failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/orders expected HTTP 200, got %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			CustomerID string `json:"customer_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("Failed to decode GET /api/orders response: %v", err)
	}
	ids := make([]string, 0, len(body.Data))
	for _, o := range body.Data {
		ids = append(ids, o.CustomerID)
	}
	return ids
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

// currentTenantStatus reads the tenant status column without failing the test so callers
// can implement their own polling/break logic.
func currentTenantStatus(t *testing.T, db *sql.DB, tenantID string) string {
	t.Helper()
	var status string
	if err := db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&status); err != nil {
		return ""
	}
	return status
}

// waitForCompanyEvent blocks until a delivery matching the routing key (and, if non-empty,
// the tenant_id) arrives on the given channel, skipping unrelated broadcasts.
func waitForCompanyEvent(t *testing.T, msgs <-chan amqp.Delivery, routingKey, tenantID string, timeout time.Duration) amqp.Delivery {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case d := <-msgs:
			var evt map[string]any
			_ = json.Unmarshal(d.Body, &evt)
			if d.RoutingKey != routingKey {
				continue
			}
			if tenantID != "" {
				if id, ok := evt["tenant_id"].(string); !ok || id != tenantID {
					continue
				}
			}
			return d
		case <-timer.C:
			t.Fatalf("Timed out waiting for '%s' broadcast for tenant '%s'", routingKey, tenantID)
		}
	}
}

// doOrderRequest issues an HTTP request with the given bearer token and returns the status
// code. A nil body performs a body-less request (e.g. GET).
func doOrderRequest(t *testing.T, method, url, authHeader string, body map[string]any) int {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewBuffer(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("Failed to build %s %s request: %v", method, url, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", authHeader)
	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}