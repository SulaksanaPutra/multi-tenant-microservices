package e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v6"
	_ "github.com/lib/pq"
	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	gatewayURL  = "http://localhost:8000/api/v1/register"
	postgresDSN = "host=localhost port=5432 user=postgres password=postgres dbname=broker_db sslmode=disable"
	rabbitmqURL = "amqp://guest:guest@localhost:5672/"
	rabbitmqAPI = "http://localhost:15672/api/exchanges/%2F/company.events"
	mailpitAPI  = "http://localhost:8025/api/v1/messages"
)

type RegisterRequest struct {
	Email      string `json:"email"`
	Name       string `json:"name"`
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
}

type RegisterResponseData struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
}

type RegisterResponse struct {
	Status  string               `json:"status"`
	Message string               `json:"message"`
	Data    RegisterResponseData `json:"data"`
}

type TenantProvisionedEvent struct {
	TenantID   string `json:"tenant_id"`
	TenantSlug string `json:"tenant_slug"`
	UserID     string `json:"user_id"`
}

type MailpitMessages struct {
	Messages []struct {
		ID      string `json:"ID"`
		Subject string `json:"Subject"`
		To      []struct {
			Address string `json:"Address"`
		} `json:"To"`
	} `json:"messages"`
}

func generateFakeTenantData() (name, email, tenantName, tenantSlug, schemaName string) {
	gofakeit.Seed(time.Now().UnixNano())

	name = gofakeit.Name()
	email = fmt.Sprintf("e2e_%d_%s", time.Now().UnixNano(), gofakeit.Email())
	tenantName = gofakeit.Company() + " " + gofakeit.CompanySuffix()

	reg := regexp.MustCompile("[^a-zA-Z0-9]+")
	cleanSlug := strings.Trim(reg.ReplaceAllString(strings.ToLower(tenantName), "_"), "_")
	tenantSlug = fmt.Sprintf("%s_%d", cleanSlug, time.Now().UnixNano()%10000)
	schemaName = "tenant_" + tenantSlug

	return name, email, tenantName, tenantSlug, schemaName
}

func TestFullMicroservicesFlow_E2E_Success(t *testing.T) {
	// 1. Setup RabbitMQ listener on topic exchange "company.events" for "tenant.provisioned" events
	rmqConn, err := amqp.Dial(rabbitmqURL)
	if err != nil {
		t.Fatalf("Failed to connect to RabbitMQ AMQP: %v", err)
	}
	defer rmqConn.Close()

	ch, err := rmqConn.Channel()
	if err != nil {
		t.Fatalf("Failed to open RabbitMQ channel: %v", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"",    // name
		false, // durable
		true,  // delete when unused
		true,  // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		t.Fatalf("Failed to declare test queue: %v", err)
	}

	err = ch.QueueBind(
		q.Name,               // queue name
		"tenant.provisioned", // routing key
		"company.events",     // exchange
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("Failed to bind queue to exchange: %v", err)
	}

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		true,   // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		t.Fatalf("Failed to consume from test queue: %v", err)
	}

	// 3. Generate realistic fake user & tenant data using gofakeit
	testName, testEmail, testTenantName, testTenantSlug, expectedSchema := generateFakeTenantData()

	t.Logf("Generated Fake Test Data ➔ Name: '%s', Email: '%s', Tenant: '%s', Slug: '%s'",
		testName, testEmail, testTenantName, testTenantSlug)

	// Register Tenant via Traefik Gateway (User Service - Step 5.2)
	reqBody, _ := json.Marshal(RegisterRequest{
		Email:      testEmail,
		Name:       testName,
		TenantName: testTenantName,
		TenantSlug: testTenantSlug,
	})

	resp, err := http.Post(gatewayURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("HTTP request to Traefik Gateway failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP status 202 Accepted, got: %d", resp.StatusCode)
	}

	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}

	if regResp.Data.UserID == "" || !strings.HasPrefix(regResp.Data.UserID, "usr_") {
		t.Fatalf("Expected valid meaningful user_id starting with 'usr_', got '%s'", regResp.Data.UserID)
	}

	if regResp.Data.TenantID != expectedSchema {
		t.Fatalf("Expected tenant_id '%s', got '%s'", expectedSchema, regResp.Data.TenantID)
	}

	t.Logf("2. [User Service] Registration accepted via Gateway! user_id='%s', tenant_id='%s'", regResp.Data.UserID, regResp.Data.TenantID)

	// 4. Verify PostgreSQL public.users and public.tenants records (Step 5.3)
	db, err := sql.Open("postgres", postgresDSN)
	if err != nil {
		t.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()

	var dbEmail, dbName string
	err = db.QueryRow("SELECT email, name FROM public.users WHERE id = $1", regResp.Data.UserID).Scan(&dbEmail, &dbName)
	if err != nil {
		t.Fatalf("Failed to find inserted user in public.users: %v", err)
	}

	var dbTenantName, dbTenantSlug, dbOwnerID string
	err = db.QueryRow("SELECT name, slug, owner_id FROM public.tenants WHERE id = $1", regResp.Data.TenantID).Scan(&dbTenantName, &dbTenantSlug, &dbOwnerID)
	if err != nil {
		t.Fatalf("Failed to find inserted tenant in public.tenants: %v", err)
	}

	if dbOwnerID != regResp.Data.UserID {
		t.Fatalf("Tenant owner_id mismatch in public.tenants! Expected '%s', got '%s'", regResp.Data.UserID, dbOwnerID)
	}

	t.Logf("3. [User Service] Verified public.users (id='%s') and public.tenants (id='%s', owner_id='%s') records!",
		regResp.Data.UserID, regResp.Data.TenantID, dbOwnerID)

	// 5. Verify RabbitMQ Management API for company.events exchange (Step 5.4)
	rmqReq, _ := http.NewRequest("GET", rabbitmqAPI, nil)
	rmqReq.SetBasicAuth("guest", "guest")
	rmqResp, err := http.DefaultClient.Do(rmqReq)
	if err != nil || rmqResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to query RabbitMQ Management API (%s): %v", rabbitmqAPI, err)
	}
	rmqResp.Body.Close()
	t.Logf("4. [RabbitMQ Broker] Verified 'company.events' exchange in RabbitMQ Management UI API!")

	// 6. Verify RabbitMQ "TenantProvisioned" event emitted by tenant-service (Step 5.6)
	select {
	case d := <-msgs:
		var event TenantProvisionedEvent
		if err := json.Unmarshal(d.Body, &event); err != nil {
			t.Fatalf("Failed to unmarshal TenantProvisioned event payload: %v", err)
		}

		if event.UserID != regResp.Data.UserID {
			t.Fatalf("Event user_id mismatch! Expected '%s', got '%s'", regResp.Data.UserID, event.UserID)
		}

		if event.TenantID != expectedSchema {
			t.Fatalf("Event tenant_id mismatch! Expected '%s', got '%s'", expectedSchema, event.TenantID)
		}

		t.Logf("5. [Tenant Service] Verified TenantProvisioned event published to RabbitMQ! tenant_id='%s', user_id='%s'", event.TenantID, event.UserID)

	case <-time.After(10 * time.Second):
		t.Fatalf("Timed out waiting for TenantProvisioned RabbitMQ event")
	}

	// 7. Verify dynamic schema & tables created by tenant-service in PostgreSQL (Step 5.5)
	var schemaExists bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)", expectedSchema).Scan(&schemaExists)
	if err != nil || !schemaExists {
		t.Fatalf("Dynamic schema '%s' does not exist in PostgreSQL!", expectedSchema)
	}

	var planValue string
	querySettings := fmt.Sprintf("SELECT setting_value FROM %s.tenant_settings WHERE setting_key = 'plan'", expectedSchema)
	err = db.QueryRow(querySettings).Scan(&planValue)
	if err != nil || planValue != "pro" {
		t.Fatalf("Tenant setting check failed inside schema '%s': %v, planValue=%s", expectedSchema, err, planValue)
	}

	// Verify member role AND profile details (name & email) inside tenant schema
	var memberUserID, memberName, memberEmail, memberRole string
	queryMember := fmt.Sprintf("SELECT user_id, name, email, role FROM %s.tenant_members WHERE user_id = $1", expectedSchema)
	err = db.QueryRow(queryMember, regResp.Data.UserID).Scan(&memberUserID, &memberName, &memberEmail, &memberRole)
	if err != nil {
		t.Fatalf("Tenant member check failed inside schema '%s': %v", expectedSchema, err)
	}

	if memberName != testName || memberEmail != testEmail || memberRole != "owner" {
		t.Fatalf("Tenant member profile mismatch inside schema '%s'! Expected name='%s', email='%s', role='owner'; Got name='%s', email='%s', role='%s'",
			expectedSchema, testName, testEmail, memberName, memberEmail, memberRole)
	}

	t.Logf("6. [Tenant Service] Verified dynamic schema '%s', tenant_settings & tenant_members profile (name='%s', email='%s', role='%s')!",
		expectedSchema, memberName, memberEmail, memberRole)

	// 8. Verify Notification Audit Log in PostgreSQL public.notifications (Step 5.7)
	var notifID int
	var notifRecipient, notifStatus string
	queryNotif := "SELECT id, recipient_email, status FROM public.notifications WHERE user_id = $1 AND tenant_id = $2"

	for i := 0; i < 10; i++ {
		err = db.QueryRow(queryNotif, regResp.Data.UserID, expectedSchema).Scan(&notifID, &notifRecipient, &notifStatus)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("Failed to find notification audit log row in public.notifications: %v", err)
	}

	if notifRecipient != testEmail || notifStatus != "sent" {
		t.Fatalf("Notification audit log mismatch! Expected recipient=%s, status=sent; Got recipient=%s, status=%s",
			testEmail, notifRecipient, notifStatus)
	}

	t.Logf("7. [Notification Service] Verified audit log in public.notifications! id=%d, user_id='%s', recipient=%s, status=%s", notifID, regResp.Data.UserID, notifRecipient, notifStatus)

	// 9. Verify Welcome Email in Mailpit via REST API (Step 5.7)
	var mailpitFound bool
	for i := 0; i < 10; i++ {
		mailResp, err := http.Get(mailpitAPI)
		if err == nil && mailResp.StatusCode == http.StatusOK {
			bodyBytes, _ := io.ReadAll(mailResp.Body)
			mailResp.Body.Close()

			var mailpitMsg MailpitMessages
			if err := json.Unmarshal(bodyBytes, &mailpitMsg); err == nil {
				for _, msg := range mailpitMsg.Messages {
					for _, to := range msg.To {
						if to.Address == testEmail {
							mailpitFound = true
							break
						}
					}
					if mailpitFound {
						break
					}
				}
			}
		}
		if mailpitFound {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !mailpitFound {
		t.Fatalf("Failed to find welcome email for %s in Mailpit REST API!", testEmail)
	}

	t.Logf("8. [Notification Service] Verified welcome email delivered to Mailpit for recipient %s!", testEmail)
}

func TestTenantRegistration_ValidationError(t *testing.T) {
	reqBody, _ := json.Marshal(RegisterRequest{
		Email:      gofakeit.Email(),
		Name:       gofakeit.Name(),
		TenantName: gofakeit.Company(),
		TenantSlug: "", // Empty slug should fail
	})

	resp, err := http.Post(gatewayURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("HTTP request to Gateway failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected HTTP status 400 Bad Request for missing tenant_slug, got: %d", resp.StatusCode)
	}

	t.Logf("Verified HTTP 400 Bad Request returned for missing tenant_slug")
}
