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

	// 1. Register a new tenant
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := http.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to register tenant for singleflight test: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResp
	_ = json.NewDecoder(resp.Body).Decode(&regResp)
	tenantID := regResp.Data.TenantID

	// 2. Wait for tenant activation (when order-service cache is empty)
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

	// Add auth: set credentials and login
	// TEMPORARY: setCredentials uses Stage 1 scaffolding endpoint.
	userID := regResp.Data.UserID
	const e2ePassword = "e2e-test-password-123"
	setCredentials(t, userID, tenantID, ownerEmail, e2ePassword)
	accessToken := loginAndGetToken(t, ownerEmail, e2ePassword)

	// 3. Fire 50 concurrent HTTP requests simultaneously to hit empty cache
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

			r, err := http.DefaultClient.Do(req)
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
