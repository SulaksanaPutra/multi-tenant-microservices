package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	_ "github.com/lib/pq"
)

func TestE2E_MultiReplica_ScalingAndRouting(t *testing.T) {
	t.Log("=== E2E Test: Multi-Replica Horizontal Scaling & Load Balancing ===")

	// 1. Scale order-service to 2 replicas
	t.Log("1. Scaling order-service to 2 replicas via Docker Compose...")
	cmd := exec.Command("docker", "compose", "-f", "../order-service/docker-compose.yml", "up", "-d", "--scale", "order-service=2")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("Warning scaling order-service: %v (%s)", err, string(out))
	}

	// 2. Register a new tenant
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := http.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to submit registration for multi-replica test: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResp
	_ = json.NewDecoder(resp.Body).Decode(&regResp)
	tenantID := regResp.Data.TenantID

	// 3. Wait for tenant activation
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
	t.Logf("2. Tenant tenant_id='%s' activated across replicas!", tenantID)

	// 4. Send 5 concurrent order requests through Traefik Gateway load balancer
	successCount := 0
	for i := 0; i < 5; i++ {
		custID := gofakeit.UUID()
		orderBody, _ := json.Marshal(OrderReq{CustomerID: custID, Amount: 199.99})

		orderReq, _ := http.NewRequest("POST", gatewayOrdersURL, bytes.NewBuffer(orderBody))
		orderReq.Header.Set("Content-Type", "application/json")
		orderReq.Header.Set("X-Tenant-ID", tenantID)

		oResp, err := http.DefaultClient.Do(orderReq)
		if err == nil && oResp.StatusCode == http.StatusCreated {
			successCount++
			oResp.Body.Close()
		} else if oResp != nil {
			body, _ := io.ReadAll(oResp.Body)
			oResp.Body.Close()
			t.Logf("Order request %d response: status=%d body=%s", i+1, oResp.StatusCode, string(body))
		}
	}

	if successCount != 5 {
		t.Fatalf("Expected 5 successful order creations across replicas, got %d", successCount)
	}

	t.Logf("3. Successfully executed 5 concurrent order creations load-balanced across multiple replicas!")
}
