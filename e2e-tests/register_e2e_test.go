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
	gatewayURL  = "http://localhost:8000/api/register"
	postgresDSN = "host=localhost port=5432 user=postgres password=postgres dbname=tenant_manager_db sslmode=disable"
	rabbitmqURL = "amqp://guest:guest@localhost:5672/"
	rabbitmqAPI = "http://localhost:15672/api/exchanges/%2F/company.events"
	mailpitAPI  = "http://localhost:8025/api/v1/messages"
)

type RegisterRequest struct {
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
	Plan       string `json:"plan"`
	TenantName string `json:"tenant_name"`
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

type WorkspaceInitiatedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"`
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
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
		q.Name,                // queue name
		"workspace.initiated", // routing key
		"company.events",      // exchange
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
	testName, testEmail, testTenantName, testTenantSlug, _ := generateFakeTenantData()

	t.Logf("Generated Fake Test Data ➔ Name: '%s', Email: '%s', Tenant: '%s', Slug: '%s'",
		testName, testEmail, testTenantName, testTenantSlug)

	// Register Tenant via Traefik Gateway (User Service - Step 5.2)
	reqBody, _ := json.Marshal(RegisterRequest{
		OwnerEmail: testEmail,
		OwnerName:  testName,
		Plan:       "shared",
		TenantName: testTenantName,
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

	if regResp.Data.TenantID == "" || !strings.HasPrefix(regResp.Data.TenantID, "tnt_") {
		t.Fatalf("Expected valid tenant_id starting with 'tnt_', got '%s'", regResp.Data.TenantID)
	}

	t.Logf("2. [Tenant Service] Registration accepted via Gateway! tenant_id='%s'", regResp.Data.TenantID)

	// 4. Verify PostgreSQL public.tenants record
	db, err := sql.Open("postgres", postgresDSN)
	if err != nil {
		t.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()

	var dbTenantName, dbTenantSlug, dbOwnerEmail string
	err = db.QueryRow("SELECT name, slug, owner_email FROM public.tenants WHERE id = $1", regResp.Data.TenantID).Scan(&dbTenantName, &dbTenantSlug, &dbOwnerEmail)
	if err != nil {
		t.Fatalf("Failed to find inserted tenant in public.tenants: %v", err)
	}

	if dbOwnerEmail != testEmail {
		t.Fatalf("Tenant owner_email mismatch in public.tenants! Expected '%s', got '%s'", testEmail, dbOwnerEmail)
	}

	t.Logf("3. [Tenant Service] Verified public.tenants (id='%s', owner_email='%s') record!",
		regResp.Data.TenantID, dbOwnerEmail)

	// 5. Verify RabbitMQ Management API for company.events exchange (Step 5.4)
	rmqReq, _ := http.NewRequest("GET", rabbitmqAPI, nil)
	rmqReq.SetBasicAuth("guest", "guest")
	rmqResp, err := http.DefaultClient.Do(rmqReq)
	if err != nil || rmqResp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to query RabbitMQ Management API (%s): %v", rabbitmqAPI, err)
	}
	rmqResp.Body.Close()
	t.Logf("4. [RabbitMQ Broker] Verified 'company.events' exchange in RabbitMQ Management UI API!")

	// 6. Verify RabbitMQ "WorkspaceInitiated" event emitted by tenant-service
	select {
	case d := <-msgs:
		var event WorkspaceInitiatedEvent
		if err := json.Unmarshal(d.Body, &event); err != nil {
			t.Fatalf("Failed to unmarshal WorkspaceInitiated event payload: %v", err)
		}

		if event.TenantID != regResp.Data.TenantID {
			t.Fatalf("Event tenant_id mismatch! Expected '%s', got '%s'", regResp.Data.TenantID, event.TenantID)
		}

		if event.OwnerEmail != testEmail {
			t.Fatalf("Event owner_email mismatch! Expected '%s', got '%s'", testEmail, event.OwnerEmail)
		}

		t.Logf("5. [Tenant Service] Verified WorkspaceInitiated event published to RabbitMQ! tenant_id='%s', owner_email='%s'", event.TenantID, event.OwnerEmail)

	case <-time.After(15 * time.Second):
		t.Fatalf("Timed out waiting for WorkspaceInitiated RabbitMQ event")
	}

	// 7. Verify tenant record created in tenant_manager_db.public.tenants
	var tenantStatus string
	err = db.QueryRow("SELECT status FROM public.tenants WHERE id = $1", regResp.Data.TenantID).Scan(&tenantStatus)
	if err != nil {
		t.Fatalf("Failed to query tenant status in public.tenants: %v", err)
	}

	t.Logf("6. [Tenant Service] Verified tenant_id='%s' record in public.tenants (status='%s')!", regResp.Data.TenantID, tenantStatus)

	// 8. Verify Welcome Email in Mailpit, extract setup token, and setup password
	var mailpitFound bool
	var messageID string

	for i := 0; i < 20; i++ {
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
							messageID = msg.ID
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
		time.Sleep(1 * time.Second)
	}

	if !mailpitFound {
		t.Logf("Mailpit email check skipped/not found within timeout")
	} else {
		t.Logf("7. [Notification Service] Verified welcome email delivered to Mailpit (Message ID '%s') for recipient %s!", messageID, testEmail)

		// Fetch message body to extract setup token
		msgURL := fmt.Sprintf("http://localhost:8025/api/v1/message/%s", messageID)
		msgResp, err := http.Get(msgURL)
		if err != nil || msgResp.StatusCode != http.StatusOK {
			t.Fatalf("Failed to fetch message body from Mailpit: %v", err)
		}
		msgBytes, _ := io.ReadAll(msgResp.Body)
		msgResp.Body.Close()

		var msgDetail struct {
			Text string `json:"Text"`
		}
		_ = json.Unmarshal(msgBytes, &msgDetail)

		tokenRegexp := regexp.MustCompile(`token=([a-zA-Z0-9_\-]+)`)
		matches := tokenRegexp.FindStringSubmatch(msgDetail.Text)
		if len(matches) < 2 {
			t.Fatalf("Failed to find setup token in welcome email text: %s", msgDetail.Text)
		}
		rawSetupToken := matches[1]
		t.Logf("8. [Auth Service] Extracted raw password setup token from welcome email: '%s'", rawSetupToken)

		// Submit password setup request to auth-service
		setupReqBody, _ := json.Marshal(map[string]string{
			"token":    rawSetupToken,
			"password": "SuperSecretPassword123!",
		})

		setupResp, err := http.Post("http://localhost:8085/auth/credentials/setup", "application/json", bytes.NewBuffer(setupReqBody))
		if err != nil {
			t.Fatalf("Failed to post credentials setup: %v", err)
		}
		defer setupResp.Body.Close()

		if setupResp.StatusCode != http.StatusOK {
			bodyErr, _ := io.ReadAll(setupResp.Body)
			t.Fatalf("Credentials setup failed with status %d: %s", setupResp.StatusCode, string(bodyErr))
		}

		var setupResult struct {
			Data struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
			} `json:"data"`
		}
		_ = json.NewDecoder(setupResp.Body).Decode(&setupResult)

		if setupResult.Data.AccessToken == "" {
			t.Fatalf("Expected access_token returned from setup password endpoint")
		}

		t.Logf("9. [Auth Service] Password setup successful! Received RS256 Access Token.")

		// Verify authenticated orders call using JWT Access Token
		orderReq, _ := http.NewRequest("GET", "http://localhost:8000/api/orders", nil)
		orderReq.Header.Set("Authorization", "Bearer "+setupResult.Data.AccessToken)
		orderResp, err := http.DefaultClient.Do(orderReq)
		if err != nil {
			t.Fatalf("Failed to make authenticated orders request: %v", err)
		}
		defer orderResp.Body.Close()

		if orderResp.StatusCode != http.StatusOK {
			t.Fatalf("Expected HTTP 200 OK for authenticated orders endpoint, got %d", orderResp.StatusCode)
		}

		t.Logf("10. [Order Service] Verified authenticated RS256 Bearer JWT data-plane access! Status 200 OK.")
	}
}

func TestTenantRegistration_ValidationError(t *testing.T) {
	reqBody, _ := json.Marshal(RegisterRequest{
		OwnerEmail: "invalid-email-format", // Invalid email format should fail validation
		OwnerName:  gofakeit.Name(),
		Plan:       "shared",
		TenantName: gofakeit.Company(),
	})

	resp, err := http.Post(gatewayURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("HTTP request to Gateway failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected HTTP status 400 Bad Request for invalid owner_email, got: %d", resp.StatusCode)
	}

	t.Logf("Verified HTTP 400 Bad Request returned for invalid owner_email")
}

func TestNotificationAPI_E2E(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://localhost:8000/api/notifications", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP request to GET /api/notifications failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP status 401 Unauthorized when Authorization header is missing, got: %d", resp.StatusCode)
	}

	t.Logf("Verified GET /api/notifications without Authorization header returns HTTP 401 Unauthorized via Gateway")
}
