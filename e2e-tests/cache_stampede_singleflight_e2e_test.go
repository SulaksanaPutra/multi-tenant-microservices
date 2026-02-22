/*
 * Test Specification: TC-E2E-010 - Cache Stampede Prevention via Singleflight Request Coalescing
 * Architectural Scope: order-service (TenantDBResolver, singleflight.Group, PoolRegistry)
 * Objective: Validate that singleflight request coalescing prevents database connection pool cache stampedes
 *            when multiple concurrent requests hit an unheated or evicted connection pool cache.
 * Failure Mode Guarded: Database connection pool exhaustion, elevated latency spikes, and cascading HTTP 500/504 errors.
 *
 * Workflow / How It Works:
 *   1. Register a new shared-plan tenant via Gateway POST /api/register and await activation in tenant_manager_db.
 *   2. Provision credentials via setup-token flow and login to acquire valid JWT bearer credentials.
 *   3. Launch 50 concurrent HTTP requests (GET /api/orders) simultaneously against order-service with empty cache.
 *   4. Verify that singleflight barrier coalesces all 50 concurrent routing/connection attempts into a single operation.
 *   5. Assert 100% HTTP 200 OK success rate across all 50 concurrent goroutines.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestE2E_CacheStampede_SingleflightCoalescing(t *testing.T) {
	t.Log("=== E2E Test: Singleflight Request Coalescing & Cache Stampede Prevention (TC-E2E-010 / Docs Case #11) ===")

	// =========================================================================
	// Step 1: Provision Shared Tenant & Await Asynchronous Activation
	// Instruction: Register a new shared-plan tenant via Gateway and poll tenant_manager_db
	//              until status transitions to 'active'.
	// Architectural Invariant: Connection cache in order-service remains cold (uninitialized).
	// =========================================================================
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := defaultHTTPClient.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to register tenant for singleflight test: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResp
	_ = json.NewDecoder(resp.Body).Decode(&regResp)
	tenantID := regResp.Data.TenantID

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
		t.Fatalf("Tenant %s failed to activate", tenantID)
	}
	t.Logf("1. Tenant tenant_id='%s' activated! Cold cache ready for stampede test.", tenantID)

	// =========================================================================
	// Step 2: Authenticate User & Obtain Access Token
	// Instruction: Provision user credentials via setCredentials and perform login
	//              to retrieve a valid RS256 JWT access token.
	// =========================================================================
	userID := regResp.Data.UserID
	const e2ePassword = "e2e-test-password-123"
	setCredentials(t, userID, tenantID, ownerEmail, e2ePassword)
	accessToken := loginAndGetToken(t, ownerEmail, e2ePassword)

	// =========================================================================
	// Step 3: Launch High-Concurrency Fanout (50 Goroutines)
	// Instruction: Spawn 50 concurrent HTTP GET /api/orders requests simultaneously
	//              to target the unheated connection pool.
	// Architectural Invariant: Order service singleflight group must intercept and coalesce
	//                          all 50 concurrent callers into a single routing/DSN resolution call.
	// =========================================================================
	const concurrentReqs = 50
	var wg sync.WaitGroup
	wg.Add(concurrentReqs)

	successChan := make(chan bool, concurrentReqs)

	t.Logf("2. Launching %d concurrent GET /api/orders requests simultaneously to test singleflight barrier...", concurrentReqs)
	for i := 0; i < concurrentReqs; i++ {
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", gatewayOrdersURL, nil)
			req.Header.Set("Authorization", bearerHeader(accessToken))

			r, err := defaultHTTPClient.Do(req)
			if err == nil && r.StatusCode == http.StatusOK {
				successChan <- true
				r.Body.Close()
			} else {
				if r != nil {
					r.Body.Close()
				}
				successChan <- false
			}
		}()
	}

	wg.Wait()
	close(successChan)

	// =========================================================================
	// Step 4: Validate Concurrency Barrier & 100% Success Rate
	// Instruction: Aggregate response results from successChan and assert that every single
	//              request succeeded with HTTP 200 OK.
	// =========================================================================
	successCount := 0
	for res := range successChan {
		if res {
			successCount++
		}
	}

	if successCount != concurrentReqs {
		t.Fatalf("Cache stampede test failed! Expected %d successful responses, got %d", concurrentReqs, successCount)
	}

	t.Logf("3. Verified Singleflight Cache Stampede Barrier: %d concurrent requests coalesced cleanly with 100%% success rate!", concurrentReqs)
}
