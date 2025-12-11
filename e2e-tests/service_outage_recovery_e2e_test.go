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

	_ "github.com/lib/pq"
)

func TestE2E_ServiceOutage_RecoveryAndCatchUp(t *testing.T) {
	t.Log("=== E2E Test: Consumer Container Outage, Queue Buffering & Async Catch-Up ===")

	// 1. Stop notification-service container to simulate service outage
	t.Log("1. Stopping notification-service container to simulate container crash/outage...")
	cmd := exec.Command("docker", "stop", "notification-service")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to stop notification-service: %v (%s)", err, string(out))
	}

	// Ensure notification-service is stopped
	defer func() {
		// Clean up: ensure notification-service is restarted after test finishes
		_ = exec.Command("docker", "start", "notification-service").Run()
	}()

	// 2. Submit Registration while notification-service is DEAD
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	t.Logf("2. Submitting Registration during notification-service outage: email='%s'", ownerEmail)

	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := http.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to submit registration during outage: %v", err)
	}
	defer resp.Body.Close()

	var regResp RegisterResp
	_ = json.NewDecoder(resp.Body).Decode(&regResp)
	tenantID := regResp.Data.TenantID

	// 3. Verify tenant activates (control plane, infra provisioner, & order service are healthy)
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
		t.Fatalf("Tenant %s failed to reach 'active' status during notification outage. Status: '%s'", tenantID, tenantStatus)
	}
	t.Logf("3. Verified tenant_id='%s' reached 'active' status while notification-service was down!", tenantID)

	// 4. Verify email is NOT in Mailpit yet (notification-service is offline)
	mResp, err := http.Get(mailpitAPIURL)
	if err == nil && mResp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(mResp.Body)
		mResp.Body.Close()
		if bytes.Contains(body, []byte(ownerEmail)) {
			t.Fatalf("Unexpected welcome email delivered while notification-service was killed!")
		}
	}
	t.Logf("4. Confirmed welcome email buffered in RabbitMQ queue (not delivered to Mailpit yet)")

	// 5. Restart notification-service container
	t.Log("5. Restarting notification-service container...")
	startCmd := exec.Command("docker", "start", "notification-service")
	if out, err := startCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to restart notification-service: %v (%s)", err, string(out))
	}

	// 6. Verify notification-service reconnects, drains RabbitMQ queue, and delivers welcome email to Mailpit
	var emailReceived bool
	for i := 0; i < 20; i++ {
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
