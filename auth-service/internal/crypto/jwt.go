package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"auth-service/internal/domain"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AccessTokenTTL is the lifetime of a JWT access token.
	// Short TTL limits exposure window if a token is stolen.
	AccessTokenTTL = 15 * time.Minute

	// RefreshTokenTTL is the lifetime of an opaque refresh token.
	RefreshTokenTTL = 7 * 24 * time.Hour
)

// JWTManager handles RS256 JWT operations using a loaded RSA key pair.
type JWTManager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

// NewJWTManager parses a PEM-encoded RSA private key string and returns a JWTManager.
func NewJWTManager(privateKeyPEM string) (*JWTManager, error) {
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("failed to parse RSA private key PEM: %w", err)
	}
	return &JWTManager{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
	}, nil
}

// NewJWTManagerFromPublicKey parses a PEM-encoded RSA public key string for verification-only use.
func NewJWTManagerFromPublicKey(publicKeyPEM string) (*JWTManager, error) {
	publicKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(publicKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("failed to parse RSA public key PEM: %w", err)
	}
	return &JWTManager{
		privateKey: nil,
		publicKey:  publicKey,
	}, nil
}

// PublicKey returns the RSA public key for external distribution (JWKS).
func (m *JWTManager) PublicKey() *rsa.PublicKey {
	return m.publicKey
}

// jwtClaims defines the full JWT payload structure registered with golang-jwt.
type jwtClaims struct {
	TenantID    string   `json:"tenant_id"`
	Email       string   `json:"email"`
	Permissions []string `json:"permissions,omitempty"`
	PermVersion int64    `json:"perm_version,omitempty"`
	jwt.RegisteredClaims
}

// SignAccessToken issues a signed RS256 JWT access token with a 15-minute TTL.
func (m *JWTManager) SignAccessToken(userID, tenantID, email, jti string, permissions []string, permVersion int64) (string, error) {
	if m.privateKey == nil {
		return "", errors.New("JWTManager is in verify-only mode (no private key loaded)")
	}
	now := time.Now().UTC()
	claims := jwtClaims{
		TenantID:    tenantID,
		Email:       email,
		Permissions: permissions,
		PermVersion: permVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
			ID:        jti,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}
	return signed, nil
}

// VerifyAccessToken parses and validates an RS256 JWT token string, returning domain.JWTClaims.
func (m *JWTManager) VerifyAccessToken(tokenString string) (*domain.JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwtClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.publicKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidCredentials, err.Error())
	}

	claims, ok := token.Claims.(*jwtClaims)
	if !ok || !token.Valid {
		return nil, domain.ErrInvalidCredentials
	}

	return &domain.JWTClaims{
		UserID:      claims.Subject,
		TenantID:    claims.TenantID,
		Email:       claims.Email,
		JTI:         claims.ID,
		Permissions: claims.Permissions,
		PermVersion: claims.PermVersion,
	}, nil
}

// GenerateRefreshToken creates a cryptographically random 256-bit opaque token.
// Returns the raw token (to be sent to the client) and its SHA-256 hex hash (to be stored in DB).
func GenerateRefreshToken() (rawToken string, tokenHash string, err error) {
	b := make([]byte, 32) // 256 bits
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("failed to generate refresh token random bytes: %w", err)
	}
	rawToken = base64.URLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash = hex.EncodeToString(hash[:])
	return rawToken, tokenHash, nil
}

// HashRefreshToken returns the SHA-256 hex hash of a raw refresh token string.
// Used on the receive path to look up the stored token record.
func HashRefreshToken(rawToken string) string {
	hash := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(hash[:])
}

// JWKSResponse is the JSON-serializable JWKS payload for /.well-known/jwks.json.
type JWKSResponse struct {
	Keys []JWK `json:"keys"`
}

// JWK represents a single JSON Web Key (RFC 7517).
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// BuildJWKS constructs a JWKS response from the loaded RSA public key.
func (m *JWTManager) BuildJWKS() ([]byte, error) {
	pub := m.publicKey
	nBytes := pub.N.Bytes()
	eBytes := big.NewInt(int64(pub.E)).Bytes()

	jwk := JWK{
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(nBytes),
		E:   base64.RawURLEncoding.EncodeToString(eBytes),
	}

	resp := JWKSResponse{Keys: []JWK{jwk}}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JWKS: %w", err)
	}
	return out, nil
}

// ParsePublicKeyPEM is a convenience helper for downstream services to strip
// header/footer lines and produce the minimal key string (used in tests).
func ParsePublicKeyPEM(pem string) string {
	lines := strings.Split(strings.TrimSpace(pem), "\n")
	if len(lines) <= 2 {
		return pem
	}
	return strings.Join(lines[1:len(lines)-1], "")
}
