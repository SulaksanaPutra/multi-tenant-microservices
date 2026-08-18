package handler_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"order-service/internal/domain"
	"order-service/internal/handler"
	"order-service/internal/infrastructure/tenantdb"
	"github.com/SulaksanaPutra/go-microservice-commons/httputil"
	"github.com/SulaksanaPutra/go-microservice-commons/middleware"
	"order-service/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// stubOrderService reproduces handler-observable service behaviour without a DB.
type stubOrderService struct{}

func (stubOrderService *stubOrderService) ListOrders(ctx context.Context) ([]service.OrderOutput, error) {
	return nil, nil
}

func (stubOrderService *stubOrderService) CreateOrder(ctx context.Context, input service.CreateOrderInput) (*service.OrderOutput, error) {
	if input.Amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}
	return nil, errors.New("no database in unit test")
}

// mockResolver implements middleware.Resolver for test use.
type mockResolver struct {
	getTenantDBFn func(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

func (m *mockResolver) GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error) {
	if m.getTenantDBFn != nil {
		return m.getTenantDBFn(ctx, tenantID)
	}
	db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable")
	if err != nil {
		return tenantdb.Config{}, err
	}
	return tenantdb.Config{
		TenantID:   tenantID,
		DB:         db,
		SchemaName: "public",
	}, nil
}

// jwtTestClaims mirrors the auth-service JWT payload.
type jwtTestClaims struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	jwt.RegisteredClaims
}

// testKeyPair generates an RSA-2048 key pair and returns the private key + PEM public key.
func testKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	return privateKey, pubPEM
}

// signJWT signs a test JWT with the given private key for a specific tenant.
func signJWT(t *testing.T, key *rsa.PrivateKey, tenantID, userID string) string {
	t.Helper()
	claims := jwtTestClaims{
		TenantID: tenantID,
		Email:    "test@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			ID:        "test-jti",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign test JWT: %v", err)
	}
	return signed
}

type tenantResolver interface {
	GetTenantDB(ctx context.Context, tenantID string) (tenantdb.Config, error)
}

func setupTestRouter(pubKeyPEM string, resolver tenantResolver) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Stub factory avoids DB coupling in unit tests; CreateOrder's amount
	// validation lives in the service, so a fake that rejects negative amounts
	// reproduces the 400 path while the success path fails non-auth (no real DB).
	orderHandler := handler.NewOrderHandler(func(cfg tenantdb.Config) handler.OrderService {
		return &stubOrderService{}
	})

	tenantHandlerHook := func(c *gin.Context, tenantID string) error {
		tenantCfg, err := resolver.GetTenantDB(c.Request.Context(), tenantID)
		if err != nil {
			httputil.WriteError(c, http.StatusInternalServerError, "failed to resolve tenant database: "+err.Error())
			return err
		}
		c.Set("tenantConfig", tenantCfg)
		ctx := tenantdb.WithConfig(c.Request.Context(), tenantCfg)
		c.Request = c.Request.WithContext(ctx)
		return nil
	}

	api := r.Group("/api")
	api.Use(middleware.RequireJWT(pubKeyPEM, middleware.WithTenantHandler(tenantHandlerHook)))

	api.POST("/orders", orderHandler.CreateOrder)
	api.GET("/orders", orderHandler.ListOrders)

	return r
}

func TestCreateOrder_MissingAuthHeader(t *testing.T) {
	_, pubKeyPEM := testKeyPair(t)
	router := setupTestRouter(pubKeyPEM, &mockResolver{})

	body := map[string]any{"customer_id": "cust-001", "amount": 99.99}
	jsonBytes, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, "/api/orders", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", w.Code)
	}
}

func TestCreateOrder_InvalidAmount(t *testing.T) {
	privateKey, pubKeyPEM := testKeyPair(t)
	router := setupTestRouter(pubKeyPEM, &mockResolver{})

	body := map[string]any{"customer_id": "cust-001", "amount": -10.0}
	jsonBytes, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, "/api/orders", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+signJWT(t, privateKey, "tenant-test", "usr_test"))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for negative amount, got %d", w.Code)
	}
}

func TestCreateOrder_ValidJWT(t *testing.T) {
	privateKey, pubKeyPEM := testKeyPair(t)
	router := setupTestRouter(pubKeyPEM, &mockResolver{})

	body := map[string]any{"customer_id": "cust-001", "amount": 150.75}
	jsonBytes, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, "/api/orders", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+signJWT(t, privateKey, "tenant-acme", "usr_acme"))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// 500 is expected in unit test (no real DB) — but NOT 401 or 403
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Errorf("expected non-auth error, got %d: %s", w.Code, w.Body.String())
	}
}

type mockTxManager struct {
	txCalled bool
}

func (m *mockTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error {
	m.txCalled = true
	return fn(ctx)
}

func TestCreateOrder_WithTransactionalExecution(t *testing.T) {
	privateKey, pubKeyPEM := testKeyPair(t)

	mockTx := &mockTxManager{}
	var serviceCalled bool

	orderHandler := handler.NewOrderHandler(
		func(cfg tenantdb.Config) handler.OrderService {
			return &functionalStubOrderService{
				createOrderFn: func(ctx context.Context, input service.CreateOrderInput) (*service.OrderOutput, error) {
					serviceCalled = true
					return &service.OrderOutput{
						ID:         "ord-123",
						TenantID:   input.TenantID,
						CustomerID: input.CustomerID,
						Amount:     input.Amount,
						Status:     "pending",
					}, nil
				},
			}
		},
		handler.WithTxManagerFactory(func(cfg tenantdb.Config) handler.TxManager {
			return mockTx
		}),
	)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	tenantHandlerHook := func(c *gin.Context, tenantID string) error {
		tenantCfg := tenantdb.Config{
			TenantID:   tenantID,
			SchemaName: "tenant_test",
		}
		c.Set("tenantConfig", tenantCfg)
		return nil
	}

	api := r.Group("/api")
	api.Use(middleware.RequireJWT(pubKeyPEM, middleware.WithTenantHandler(tenantHandlerHook)))
	api.POST("/orders", orderHandler.CreateOrder)

	body := map[string]any{"customer_id": "cust-tx-001", "amount": 100.0}
	jsonBytes, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, "/api/orders", bytes.NewBuffer(jsonBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+signJWT(t, privateKey, "tenant-test", "usr_test"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	if !mockTx.txCalled {
		t.Error("expected TxManager.WithTransaction to be invoked during CreateOrder")
	}
	if !serviceCalled {
		t.Error("expected OrderService.CreateOrder to be invoked inside transaction")
	}
}

type functionalStubOrderService struct {
	createOrderFn func(ctx context.Context, input service.CreateOrderInput) (*service.OrderOutput, error)
}

func (s *functionalStubOrderService) ListOrders(ctx context.Context) ([]service.OrderOutput, error) {
	return nil, nil
}

func (s *functionalStubOrderService) CreateOrder(ctx context.Context, input service.CreateOrderInput) (*service.OrderOutput, error) {
	if s.createOrderFn != nil {
		return s.createOrderFn(ctx, input)
	}
	return nil, nil
}
