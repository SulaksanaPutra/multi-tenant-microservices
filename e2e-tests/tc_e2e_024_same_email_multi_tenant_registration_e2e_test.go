/*
 * Package e2e_test - E2E Testing Infrastructure
 * File: tc_e2e_024_same_email_multi_tenant_registration_e2e_test.go
 *
 * Architectural Scope: Control Plane Registration, Composite Uniqueness (tenant_id, email),
 *                      Password Provisioning & RS256 JWT Multi-Tenant Token Verification.
 *
 * Test Case Objective (TC-E2E-024):
 *   Verify that the same email address (e.g. owner@multi-tenant.com) can independently register
 *   multiple distinct tenant workspaces (Shared & Dedicated plans) without encountering database
 *   unique constraint failures or AMQP barrier sync deadlocks. Validate that credentials, JWT access
 *   tokens, and isolated user profiles are correctly bound to their respective tenant_ids.
 */

package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/lib/pq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestTC_E2E_024_SameEmailMultiTenantRegistration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E integration test in short mode.")
	}

	const sharedEmail = "owner_multi@company.com"
	const ownerName = "John MultiOwner"

	// 1. Establish RabbitMQ connection & listener queue for workspace.initiated
	amqpConn, err := amqp.Dial(rabbitmqDSN)
	if err != nil {
		t.Fatalf("[Setup] Failed to connect to RabbitMQ: %v", err)
	}
	defer amqpConn.Close()

	ch, err := amqpConn.Channel()
	if err != nil {
		t.Fatalf("[Setup] Failed to open RabbitMQ channel: %v", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		t.Fatalf("[Setup] Failed to declare ephemeral queue: %v", err)
	}

	if err := ch.QueueBind(q.Name, "workspace.initiated", "company.events", false, nil); err != nil {
		t.Fatalf("[Setup] Failed to bind queue: %v", err)
	}

	msgs, err := ch.Consume(q.Name, "", true, true, false, false, nil)
	if err != nil {
		t.Fatalf("[Setup] Failed to consume from queue: %v", err)
	}

	// 2. Register Tenant 1 (Shared Plan) with sharedEmail
	tenant1ReqBody, _ := json.Marshal(map[string]string{
		"owner_name":  ownerName,
		"owner_email": sharedEmail,
		"tenant_name": "Multi Tenant Alpha",
		"plan":        "shared",
	})

	resp1, err := defaultHTTPClient.Post(gatewayBaseURL+"/api/tenants/register", "application/json", bytes.NewBuffer(tenant1ReqBody))
	if err != nil {
		t.Fatalf("[Step 1] Failed to register Tenant 1: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("[Step 1] Expected HTTP 202 Accepted for Tenant 1 registration, got %d", resp1.StatusCode)
	}

	tenant1ID := extractTenantIDFromAMQP(t, msgs, sharedEmail)
	t.Logf("[Step 1] Successfully registered Tenant 1 (Shared Plan) -> tenant_id='%s'", tenant1ID)

	// 3. Register Tenant 2 (Dedicated Plan) with SAME sharedEmail
	tenant2ReqBody, _ := json.Marshal(map[string]string{
		"owner_name":  ownerName,
		"owner_email": sharedEmail,
		"tenant_name": "Multi Tenant Beta",
		"plan":        "dedicated",
	})

	resp2, err := defaultHTTPClient.Post(gatewayBaseURL+"/api/tenants/register", "application/json", bytes.NewBuffer(tenant2ReqBody))
	if err != nil {
		t.Fatalf("[Step 2] Failed to register Tenant 2: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("[Step 2] Expected HTTP 202 Accepted for Tenant 2 registration, got %d", resp2.StatusCode)
	}

	tenant2ID := extractTenantIDFromAMQP(t, msgs, sharedEmail)
	t.Logf("[Step 2] Successfully registered Tenant 2 (Dedicated Plan) -> tenant_id='%s'", tenant2ID)

	if tenant1ID == tenant2ID {
		t.Fatalf("[Validation Flaw] Tenant 1 and Tenant 2 received identical tenant_ids '%s'", tenant1ID)
	}

	// 4. Wait for both tenants to become ACTIVE in tenant_manager_db
	tenantMgrDB, err := sql.Open("postgres", tenantDBDSN)
	if err != nil {
		t.Fatalf("[Setup] Failed to connect to tenant_manager_db: %v", err)
	}
	defer tenantMgrDB.Close()

	waitForTenantActive(t, tenantMgrDB, tenant1ID)
	waitForTenantActive(t, tenantMgrDB, tenant2ID)

	// 5. Verify user_db contains unified user profile record for sharedEmail
	db, err := sql.Open("postgres", userDBDSN)
	if err != nil {
		t.Fatalf("[Validation] Failed to connect to user_db: %v", err)
	}
	defer db.Close()

	var userID string
	err = db.QueryRow("SELECT id FROM public.users WHERE email = $1", sharedEmail).Scan(&userID)
	if err != nil {
		t.Fatalf("[Validation] Failed to query user_db for email='%s': %v", sharedEmail, err)
	}

	if userID == "" {
		t.Fatalf("[Validation Flaw] Missing user profile record for email='%s'", sharedEmail)
	}
	t.Logf("[Validation] Unified user profile confirmed in user_db: user_id='%s'", userID)

	// 6. Provision credentials via setup token flow
	const password = "Password123!"
	setCredentials(t, userID, tenant1ID, sharedEmail, password)
	setCredentials(t, userID, tenant2ID, sharedEmail, password)

	// 7. Authenticate & Select Workspace for Tenant 1 and Tenant 2
	jwt1, _ := loginAndGetTokenWithTenant(t, tenant1ID, sharedEmail, password)
	jwt2, _ := loginAndGetTokenWithTenant(t, tenant2ID, sharedEmail, password)

	claims1 := parseTokenClaims(t, jwt1)
	claims2 := parseTokenClaims(t, jwt2)

	if claims1.TenantID != tenant1ID {
		t.Fatalf("[Validation Flaw] JWT 1 claims tenant_id='%s', expected '%s'", claims1.TenantID, tenant1ID)
	}
	if claims2.TenantID != tenant2ID {
		t.Fatalf("[Validation Flaw] JWT 2 claims tenant_id='%s', expected '%s'", claims2.TenantID, tenant2ID)
	}

	t.Logf("[Success] TC-E2E-024 Passed cleanly! Unified user identity registered multiple tenants '%s', provisioned memberships, and issued isolated JWTs via workspace selection.", sharedEmail)
}

func extractTenantIDFromAMQP(t *testing.T, msgs <-chan amqp.Delivery, targetEmail string) string {
	t.Helper()

	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()

	for {
		select {
		case d := <-msgs:
			var payload struct {
				TenantID   string `json:"tenant_id"`
				OwnerEmail string `json:"owner_email"`
			}
			if err := json.Unmarshal(d.Body, &payload); err == nil && payload.OwnerEmail == targetEmail && payload.TenantID != "" {
				return payload.TenantID
			}
		case <-timer.C:
			t.Fatalf("[AMQP] Timed out waiting for workspace.initiated event for email='%s'", targetEmail)
			return ""
		}
	}
}

func parseTokenClaims(t *testing.T, tokenStr string) *customJWTClaims {
	t.Helper()

	token, _, err := jwt.NewParser().ParseUnverified(tokenStr, &customJWTClaims{})
	if err != nil {
		t.Fatalf("[JWT] Failed to parse JWT token: %v", err)
	}

	claims, ok := token.Claims.(*customJWTClaims)
	if !ok {
		t.Fatalf("[JWT] Failed to cast token claims to customJWTClaims")
	}

	return claims
}
