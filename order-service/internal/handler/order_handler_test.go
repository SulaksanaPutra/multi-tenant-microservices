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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"order-service/internal/handler"
	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/middleware"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

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

func setupTestRouter(pubKeyPEM string, resolver middleware.Resolver) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	orderHandler := handler.NewOrderHandler(nil)

	api := r.Group("/api")
	api.Use(middleware.RequireJWT(pubKeyPEM, resolver))

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
