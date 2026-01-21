package crypto

import (
	"testing"
)

func TestDeriveTenantDBPassword(t *testing.T) {
	t.Run("default secret fallback when empty", func(t *testing.T) {
		pwd1 := DeriveTenantDBPassword("", "tenant-infra")
		pwd2 := DeriveTenantDBPassword("default_shared_db_secret_key", "tenant-infra")
		if pwd1 != pwd2 {
			t.Errorf("expected empty secret fallback to match default secret, got %s vs %s", pwd1, pwd2)
		}
	})

	t.Run("determinism", func(t *testing.T) {
		pwd1 := DeriveTenantDBPassword("provisioner-secret", "tenant-abc")
		pwd2 := DeriveTenantDBPassword("provisioner-secret", "tenant-abc")
		if pwd1 != pwd2 {
			t.Errorf("expected deterministic password, got %s vs %s", pwd1, pwd2)
		}
	})

	t.Run("uniqueness for different tenants", func(t *testing.T) {
		pwd1 := DeriveTenantDBPassword("provisioner-secret", "tenant-1")
		pwd2 := DeriveTenantDBPassword("provisioner-secret", "tenant-2")
		if pwd1 == pwd2 {
			t.Errorf("expected different passwords for different tenants, got same: %s", pwd1)
		}
	})

	t.Run("formatting", func(t *testing.T) {
		pwd := DeriveTenantDBPassword("provisioner-secret", "tenant-xyz")
		if len(pwd) != 27 { // "pg_" (3) + 24 hex chars = 27
			t.Errorf("expected password length 27, got %d (%s)", len(pwd), pwd)
		}
		if pwd[:3] != "pg_" {
			t.Errorf("expected prefix pg_, got %s", pwd[:3])
		}
	})
}
