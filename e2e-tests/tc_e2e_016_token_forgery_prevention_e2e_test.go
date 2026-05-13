/*
 * Test Specification: TC-E2E-016 - Malicious Token Forgery Prevention (RS256 vs HS256 Algorithm Confusion Attack)
 * Architectural Scope: order-service (JWT Middleware / RequireJWT)
 * Objective: Validate Zero-Trust cryptographic boundary by ensuring downstream services strictly enforce RS256
 *            asymmetric verification and unconditionally reject forged symmetric signatures (algorithm confusion attack).
 * Failure Mode Guarded: JWT algorithm confusion vulnerability (signing HS256 tokens using public key string as secret).
 *
 * Workflow / How It Works:
 *   1. Fetch public JWKS key from auth-service endpoint /.well-known/jwks.json and convert to PEM format.
 *   2. Forge a JWT payload with valid tenant/user claims, but sign it using HS256 (HMAC-SHA256) with the public key PEM string as the secret.
 *   3. Issue GET /api/orders with the forged token bearer header.
 *   4. Assert downstream JWT middleware rejects the request with HTTP 401 Unauthorized before executing domain logic.
 */

package e2e_test

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWKSResponse maps the JSON payload from GET /.well-known/jwks.json.
type JWKSResponse struct {
	Keys []struct {
		Kty string `json:"kty"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// fetchPublicKeyFromJWKS calls GET /.well-known/jwks.json, reconstructs the
// RSA public key from the first JWK entry, and returns it as a *rsa.PublicKey.
//
// Instruction:
//  1. Query GET /.well-known/jwks.json.
//  2. Base64url-decode modulus 'n' and exponent 'e'.
//  3. Return constructed *rsa.PublicKey.
func fetchPublicKeyFromJWKS(t *testing.T) *rsa.PublicKey {
	t.Helper()

	resp, err := defaultHTTPClient.Get(authServiceURL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatalf("[JWKS] Failed to fetch JWKS: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("[JWKS] /.well-known/jwks.json returned status %d", resp.StatusCode)
	}

	var jwks JWKSResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil || len(jwks.Keys) == 0 {
		t.Fatalf("[JWKS] Failed to decode JWKS or keys array is empty: %v", err)
	}

	key := jwks.Keys[0]

	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		t.Fatalf("[JWKS] Failed to decode JWK modulus 'n': %v", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		t.Fatalf("[JWKS] Failed to decode JWK exponent 'e': %v", err)
	}

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}
}

// publicKeyToPEM marshals an *rsa.PublicKey to PEM-encoded bytes.
func publicKeyToPEM(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("[JWKS] Failed to marshal public key to PKIX DER: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func TestE2E_TC_E2E_016_MaliciousTokenForgeryPrevention(t *testing.T) {
	t.Log("=== TC-E2E-016: Malicious Token Forgery Prevention (RS256 vs HS256 Algorithm Confusion) ===")

	// =========================================================================
	// Step 1: Provision Shared Tenant & Await Activation
	// Instruction: Register tenant via Gateway POST /api/register and poll tenant_manager_db
	//              until status transitions to 'active'.
	// =========================================================================
	tenantID, userID, ownerEmail, _ := registerAndActivateTenant(t)
	t.Logf("2. Tenant '%s' is active.", tenantID)

	// =========================================================================
	// Step 2: Fetch Public Key from Auth Service JWKS Endpoint
	// Instruction: Fetch RSA public key from GET /.well-known/jwks.json and convert to PEM format.
	// =========================================================================
	pubKey := fetchPublicKeyFromJWKS(t)
	pubKeyPEM := publicKeyToPEM(t, pubKey)
	t.Log("3. RSA public key fetched from /.well-known/jwks.json.")

	// =========================================================================
	// Step 3: Execute Algorithm Confusion Attack Simulation
	// Instruction: Construct custom JWT payload with valid tenant/user claims, but sign using
	//              jwt.SigningMethodHS256 with pubKeyPEM as the symmetric HMAC key.
	// Architectural Invariant: Vulnerable JWT parsers check HMAC using public key bytes and accept;
	//                          secure parsers verify alg header is strictly RS256.
	// =========================================================================
	now := time.Now().UTC()
	forgedClaims := customJWTClaims{
		TenantID: tenantID,
		Email:    ownerEmail,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			ID:        "forged-hs256-jti",
		},
	}
	forgedTokenStr, err := jwt.NewWithClaims(jwt.SigningMethodHS256, forgedClaims).SignedString(pubKeyPEM)
	if err != nil {
		t.Fatalf("Failed to sign forged HS256 JWT: %v", err)
	}
	t.Log("4. Forged HS256 token created using RSA public key as HMAC secret.")

	// =========================================================================
	// Step 4: Verify Zero-Trust Rejection of Forged Token
	// Instruction: Submit GET /api/orders with forged token bearer header and assert HTTP 401 Unauthorized.
	// Architectural Invariant: RequireJWT middleware explicitly rejects non-RS256 signing algorithms.
	// =========================================================================
	req, err := http.NewRequest(http.MethodGet, gatewayOrdersURL, nil)
	if err != nil {
		t.Fatalf("Failed to create GET /api/orders request: %v", err)
	}
	req.Header.Set("Authorization", bearerHeader(forgedTokenStr))

	respForged, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/orders with forged token failed: %v", err)
	}
	defer respForged.Body.Close()

	if respForged.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected HTTP 401 Unauthorized for forged HS256 token, got %d", respForged.StatusCode)
	}
	t.Log("5. Success: Algorithm-confusion attack (HS256 vs RS256) correctly rejected with HTTP 401 Unauthorized!")
}
