package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	_ "github.com/lib/pq"
)

func TestE2E_DedicatedPlan_FullWorkflow(t *testing.T) {
	t.Log("=== E2E Test: Dedicated Plan Dynamic Docker Container Provisioning & Order Flow ===")

	// 1. Submit Registration for Dedicated Plan
	ownerName, ownerEmail, tenantName, _ := generateFakeData("dedicated")
	t.Logf("1. Submitting Dedicated Plan Registration: owner='%s', email='%s', tenant='%s'", ownerName, ownerEmail, tenantName)

	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "dedicated",
		TenantName: tenantName,
	})

	resp, err := http.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("HTTP POST /api/register failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP 202 Accepted for dedicated registration, got %d", resp.StatusCode)
	}

	var regResp RegisterResp
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("Failed to decode register response: %v", err)
	}
	tenantID := regResp.Data.TenantID
	if tenantID == "" || !strings.HasPrefix(tenantID, "tnt_") {
		t.Fatalf("Expected valid tenant_id starting with 'tnt_', got '%s'", tenantID)
	}
	t.Logf("2. Dedicated Tenant registration accepted! tenant_id='%s'", tenantID)

	// 2. Poll database for Tenant Status = ACTIVE (giving infra-provisioner time to spin up Docker container)
	db, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to tenant_manager_db: %v", err)
	}
	defer db.Close()

	var tenantStatus string
	activated := false
	for i := 0; i < 30; i++ { // Give up to 15 seconds for Docker container provisioning
		err := db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", tenantID).Scan(&tenantStatus)
		if err == nil && tenantStatus == "active" {
			activated = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !activated {
		t.Fatalf("Dedicated Tenant %s failed to reach 'active' status. Final status: '%s'", tenantID, tenantStatus)
	}
	t.Logf("3. Verified dedicated container provisioned & tenant_id='%s' reached 'active' status!", tenantID)

	// 3. Verify Routing Metadata in tenant_infrastructures
	var dbHost string
	var dbPort int
	err = db.QueryRow("SELECT db_host, db_port FROM public.tenant_infrastructures WHERE tenant_id = $1 AND service_name = 'order-service'", tenantID).Scan(&dbHost, &dbPort)
	if err != nil {
		t.Fatalf("Failed to find routing metadata in tenant_infrastructures: %v", err)
	}
	t.Logf("4. Verified routing metadata for dedicated DB: host='%s', port=%d", dbHost, dbPort)

	// 4. Create Order via Gateway on Dedicated Tenant DB Container
	custID := gofakeit.UUID()
	orderBody, _ := json.Marshal(OrderReq{
		CustomerID: custID,
		Amount:     499.99,
	})

	orderReq, _ := http.NewRequest("POST", gatewayOrdersURL, bytes.NewBuffer(orderBody))
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("X-Tenant-ID", tenantID)

	oResp, err := http.DefaultClient.Do(orderReq)
	if err != nil {
		t.Fatalf("POST /api/orders targeting dedicated tenant failed: %v", err)
	}
	defer oResp.Body.Close()

	if oResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(oResp.Body)
		t.Fatalf("Expected HTTP 201 Created for order creation on dedicated DB, got %d: %s", oResp.StatusCode, string(respBody))
	}

	var createOrderResp OrderResp
	if err := json.NewDecoder(oResp.Body).Decode(&createOrderResp); err != nil {
		t.Fatalf("Failed to decode order response: %v", err)
	}
	orderID := createOrderResp.Data.ID
	t.Logf("5. Successfully created order id='%s' on dedicated tenant DB container!", orderID)

	// 5. Fetch Orders via Gateway on Dedicated Tenant DB Container
	getOrdersReq, _ := http.NewRequest("GET", gatewayOrdersURL, nil)
	getOrdersReq.Header.Set("X-Tenant-ID", tenantID)

	getResp, err := http.DefaultClient.Do(getOrdersReq)
	if err != nil || getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/orders failed on dedicated DB or returned non-200 status: %v", getResp)
	}
	defer getResp.Body.Close()

	var listResp ListOrdersResp
	if err := json.NewDecoder(getResp.Body).Decode(&listResp); err != nil {
		t.Fatalf("Failed to decode list orders response: %v", err)
	}

	if len(listResp.Data) == 0 {
		t.Fatalf("Expected at least 1 order for dedicated tenant_id='%s', got 0", tenantID)
	}
	t.Logf("6. Verified GET /api/orders returned %d order(s) for dedicated tenant_id='%s'!", len(listResp.Data), tenantID)
}
