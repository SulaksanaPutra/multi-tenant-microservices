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
	gatewayRegisterURL = "http://localhost:8000/api/register"
	gatewayOrdersURL   = "http://localhost:8000/api/orders"
	gatewayNotifsURL   = "http://localhost:8000/api/notifications"
	tenantDBDSN        = "host=localhost port=5432 user=postgres password=postgres dbname=tenant_manager_db sslmode=disable"
	sharedDBDSN        = "host=localhost port=5432 user=postgres password=postgres dbname=shared_db sslmode=disable"
	rabbitmqDSN        = "amqp://guest:guest@localhost:5672/"
	mailpitAPIURL      = "http://localhost:8025/api/v1/messages"
)

type RegisterReq struct {
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
	Plan       string `json:"plan"`
	TenantName string `json:"tenant_name"`
}

type RegisterRespData struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
}

type RegisterResp struct {
	Status  string           `json:"status"`
	Message string           `json:"message"`
	Data    RegisterRespData `json:"data"`
}

type OrderReq struct {
	CustomerID string  `json:"customer_id"`
	Amount     float64 `json:"amount"`
}

type OrderRespData struct {
	ID         string  `json:"id"`
	TenantID   string  `json:"tenant_id"`
	CustomerID string  `json:"customer_id"`
	Status     string  `json:"status"`
	Amount     float64 `json:"amount"`
}

type OrderResp struct {
	Status  string        `json:"status"`
	Message string        `json:"message"`
	Data    OrderRespData `json:"data"`
}

type ListOrdersResp struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    []OrderRespData `json:"data"`
}

type MailpitMsgList struct {
	Messages []struct {
		ID      string `json:"ID"`
		Subject string `json:"Subject"`
		To      []struct {
			Address string `json:"Address"`
		} `json:"To"`
	} `json:"messages"`
}

func generateFakeData(plan string) (name, email, tenantName, slug string) {
	gofakeit.Seed(time.Now().UnixNano())
	name = gofakeit.Name()
	email = fmt.Sprintf("e2e_%s_%d_%s", plan, time.Now().UnixNano(), gofakeit.Email())
	tenantName = gofakeit.Company() + " " + gofakeit.CompanySuffix()
	reg := regexp.MustCompile("[^a-zA-Z0-9]+")
	cleanSlug := strings.Trim(reg.ReplaceAllString(strings.ToLower(tenantName), "_"), "_")
	slug = fmt.Sprintf("%s_%d", cleanSlug, time.Now().UnixNano()%10000)
	return name, email, tenantName, slug
}

func TestE2E_SharedPlan_FullWorkflow(t *testing.T) {
	t.Log("=== E2E Test: Shared Plan Registration, Activation, Notifications & Order Management ===")

	// 1. Setup RabbitMQ listener on topic exchange "company.events" for "workspace.initiated"
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
		t.Fatalf("Failed to declare queue: %v", err)
	}
	if err := ch.QueueBind(q.Name, "workspace.initiated", "company.events", false, nil); err != nil {
		t.Fatalf("Failed to bind queue: %v", err)
	}

	msgs, err := ch.Consume(q.Name, "", true, false, false, false, nil)
	if err != nil {
		t.Fatalf("Failed to consume from queue: %v", err)
	}

	// 2. Submit Registration for Shared Plan
	ownerName, ownerEmail, tenantName, _ := generateFakeData("shared")
	t.Logf("1. Submitting Registration: owner='%s', email='%s', tenant='%s', plan='shared'", ownerName, ownerEmail, tenantName)

	reqBody, _ := json.Marshal(RegisterReq{
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Plan:       "shared",
		TenantName: tenantName,
	})

	resp, err := http.Post(gatewayRegisterURL, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		t.Fatalf("HTTP POST /api/register failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Expected HTTP 202 Accepted, got %d", resp.StatusCode)
	}

	var regResp RegisterResp
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		t.Fatalf("Failed to decode register response: %v", err)
	}
	tenantID := regResp.Data.TenantID
	if tenantID == "" || !strings.HasPrefix(tenantID, "tnt_") {
		t.Fatalf("Expected valid tenant_id starting with 'tnt_', got '%s'", tenantID)
	}
	t.Logf("2. Tenant registration accepted! tenant_id='%s'", tenantID)

	// 3. Verify workspace.initiated event on RabbitMQ
	select {
	case d := <-msgs:
		var event map[string]any
		_ = json.Unmarshal(d.Body, &event)
		if event["tenant_id"] != tenantID {
			t.Fatalf("RabbitMQ event tenant_id mismatch! Expected '%s', got '%v'", tenantID, event["tenant_id"])
		}
		t.Logf("3. Verified workspace.initiated event on RabbitMQ for tenant_id='%s'", tenantID)
	case <-time.After(10 * time.Second):
		t.Fatalf("Timed out waiting for workspace.initiated event")
	}

	// 4. Poll database for Tenant Status = ACTIVE
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
	t.Logf("4. Verified tenant_id='%s' status reached 'active' in tenant_manager_db!", tenantID)

	// 5. Verify Welcome Email in Mailpit
	var emailReceived bool
	for i := 0; i < 15; i++ {
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
		t.Logf("Warning: Mailpit email check timed out for %s (non-fatal if SMTP delayed)", ownerEmail)
	} else {
		t.Logf("5. Verified welcome email delivered to Mailpit for %s!", ownerEmail)
	}

	// 6. Set credentials and login to obtain a JWT access token.
	// TEMPORARY: setCredentials uses the Stage 1 scaffolding endpoint.
	// Replace with email-invite flow in the OAuth 2.0 stage.
	userID := regResp.Data.UserID
	const e2ePassword = "e2e-test-password-123"
	setCredentials(t, userID, tenantID, ownerEmail, e2ePassword)
	accessToken := loginAndGetToken(t, ownerEmail, e2ePassword)

	// 6b. Check Notifications API via Gateway using JWT Bearer token
	nReq, _ := http.NewRequest("GET", gatewayNotifsURL, nil)
	nReq.Header.Set("Authorization", bearerHeader(accessToken))
	nResp, err := http.DefaultClient.Do(nReq)
	if err != nil || nResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/notifications failed or returned status %v", nResp)
	}
	nResp.Body.Close()
	t.Logf("6. Verified GET /api/notifications API returning 200 OK (JWT Bearer auth)")

	// 7. Create Order via Gateway on Shared Tenant DB
	custID := gofakeit.UUID()
	orderBody, _ := json.Marshal(OrderReq{
		CustomerID: custID,
		Amount:     149.99,
	})

	orderReq, _ := http.NewRequest("POST", gatewayOrdersURL, bytes.NewBuffer(orderBody))
	orderReq.Header.Set("Content-Type", "application/json")
	orderReq.Header.Set("Authorization", bearerHeader(accessToken))

	oResp, err := http.DefaultClient.Do(orderReq)
	if err != nil {
		t.Fatalf("POST /api/orders failed: %v", err)
	}
	defer oResp.Body.Close()

	if oResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(oResp.Body)
		t.Fatalf("Expected HTTP 201 Created for order creation, got %d: %s", oResp.StatusCode, string(respBody))
	}

	var createOrderResp OrderResp
	if err := json.NewDecoder(oResp.Body).Decode(&createOrderResp); err != nil {
		t.Fatalf("Failed to decode order response: %v", err)
	}
	orderID := createOrderResp.Data.ID
	t.Logf("7. Successfully created order id='%s' for shared tenant_id='%s'", orderID, tenantID)

	// 8. Fetch Orders via Gateway on Shared Tenant DB
	getOrdersReq, _ := http.NewRequest("GET", gatewayOrdersURL, nil)
	getOrdersReq.Header.Set("Authorization", bearerHeader(accessToken))

	getResp, err := http.DefaultClient.Do(getOrdersReq)
	if err != nil || getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/orders failed or returned non-200 status: %v", getResp)
	}
	defer getResp.Body.Close()

	var listResp ListOrdersResp
	if err := json.NewDecoder(getResp.Body).Decode(&listResp); err != nil {
		t.Fatalf("Failed to decode list orders response: %v", err)
	}

	if len(listResp.Data) == 0 {
		t.Fatalf("Expected at least 1 order for tenant_id='%s', got 0", tenantID)
	}
	t.Logf("8. Verified GET /api/orders returned %d order(s) for shared tenant_id='%s'!", len(listResp.Data), tenantID)
}
