package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// DeriveTenantDBPassword computes the deterministic password for a tenant database using HMAC-SHA256.
func DeriveTenantDBPassword(secret, tenantID string) string {
	if secret == "" {
		secret = "default_shared_db_secret_key"
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte("tenant_db_v1_" + tenantID))
	return fmt.Sprintf("pg_%s", hex.EncodeToString(h.Sum(nil))[:24])
}
