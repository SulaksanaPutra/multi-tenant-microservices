package docker

import (
	"os/exec"
	"strings"
	"testing"
)

func TestProvisionTenantDatabaseStatements(t *testing.T) {
	t.Run("fresh_provision_creates_role_and_database", func(t *testing.T) {
		stmts, err := provisionTenantDatabaseStatements("tnt_acme_order_db", "tnt_acme_order_user", "pg_secret", false, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		join := strings.Join(stmts.maintenance, " ")
		want := []string{
			`CREATE ROLE "tnt_acme_order_user" LOGIN PASSWORD 'pg_secret';`,
			`CREATE DATABASE "tnt_acme_order_db" OWNER "tnt_acme_order_user";`,
			`REVOKE CONNECT ON DATABASE "tnt_acme_order_db" FROM PUBLIC;`,
			`GRANT CONNECT ON DATABASE "tnt_acme_order_db" TO "tnt_acme_order_user";`,
		}
		for _, w := range want {
			if !strings.Contains(join, w) {
				t.Errorf("expected maintenance statements to contain %q, got: %s", w, join)
			}
		}
		if got := strings.Join(stmts.target, " "); !strings.Contains(got, `GRANT ALL ON SCHEMA public TO "tnt_acme_order_user";`) {
			t.Errorf("expected target schema grant, got: %s", got)
		}
	})

	t.Run("existing_role_alters_password", func(t *testing.T) {
		stmts, err := provisionTenantDatabaseStatements("tnt_acme_order_db", "tnt_acme_order_user", "pg_secret", true, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		join := strings.Join(stmts.maintenance, " ")
		if !strings.Contains(join, `ALTER ROLE "tnt_acme_order_user" WITH PASSWORD 'pg_secret';`) {
			t.Errorf("expected ALTER ROLE for existing role, got: %s", join)
		}
		if strings.Contains(join, "CREATE ROLE") {
			t.Errorf("should not CREATE ROLE when it already exists: %s", join)
		}
		if strings.Contains(join, "CREATE DATABASE") {
			t.Errorf("should not CREATE DATABASE when it already exists: %s", join)
		}
	})

	t.Run("invalid_identifiers_rejected", func(t *testing.T) {
		if _, err := provisionTenantDatabaseStatements("bad-name!order_db", "role", "pass", false, false); err == nil {
			t.Error("expected error for invalid database name")
		}
		if _, err := provisionTenantDatabaseStatements("db", "bad;role", "pass", false, false); err == nil {
			t.Error("expected error for invalid role name")
		}
	})
}

func TestDropTenantDatabaseStatements(t *testing.T) {
	t.Run("drops_database_owned_and_role", func(t *testing.T) {
		stmts, err := dropTenantDatabaseStatements("tnt_acme_order_db", "tnt_acme_order_user", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		join := strings.Join(stmts, " ")
		for _, w := range []string{
			`DROP DATABASE IF EXISTS "tnt_acme_order_db" WITH (FORCE);`,
			`DROP OWNED BY "tnt_acme_order_user";`,
			`DROP ROLE IF EXISTS "tnt_acme_order_user";`,
		} {
			if !strings.Contains(join, w) {
				t.Errorf("expected statements to contain %q, got: %s", w, join)
			}
		}
	})

	t.Run("skips_drop_owned_when_role_missing", func(t *testing.T) {
		stmts, err := dropTenantDatabaseStatements("tnt_acme_order_db", "tnt_acme_order_user", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		join := strings.Join(stmts, " ")
		if strings.Contains(join, "DROP OWNED") {
			t.Errorf("should not emit DROP OWNED for a missing role: %s", join)
		}
		if !strings.Contains(join, "DROP ROLE IF EXISTS") {
			t.Errorf("expected DROP ROLE IF EXISTS: %s", join)
		}
	})

	t.Run("invalid_identifiers_rejected", func(t *testing.T) {
		if _, err := dropTenantDatabaseStatements("bad-name!db", "role", true); err == nil {
			t.Error("expected error for invalid database name")
		}
		if _, err := dropTenantDatabaseStatements("db", "bad;role", true); err == nil {
			t.Error("expected error for invalid role name")
		}
	})
}

func TestBuildSchemaRewriteSed_UpgradeLockedToPublic(t *testing.T) {
	if _, err := exec.LookPath("sed"); err != nil {
		t.Skip("sed not available in test environment")
	}

	src := "tnt_acme_order_db_locked"
	dst := "public"
	input := `CREATE SCHEMA tnt_acme_order_db_locked;
SET search_path = tnt_acme_order_db_locked, pg_catalog;
CREATE TABLE tnt_acme_order_db_locked.orders (id text);
CREATE INDEX idx_orders_status ON tnt_acme_order_db_locked.orders USING btree (status);
COPY tnt_acme_order_db_locked.orders (id, customer_id) FROM stdin;
1	customer_1
\.

`

	sedProg := buildSchemaRewriteSed(src, dst)
	cmd := exec.Command("sed", sedProg)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sed failed: %v", err)
	}
	got := string(out)

	wantContains := []string{
		"CREATE SCHEMA IF NOT EXISTS public;",
		"SET search_path = public, pg_catalog;",
		"CREATE TABLE public.orders (id text);",
		"CREATE INDEX idx_orders_status ON public.orders USING btree (status);",
		"COPY public.orders (id, customer_id) FROM stdin;",
	}
	for _, w := range wantContains {
		if !strings.Contains(got, w) {
			t.Errorf("expected output to contain %q, got:\n%s", w, got)
		}
	}
	if strings.Contains(got, src) {
		t.Errorf("output still contains source schema %q:\n%s", src, got)
	}
}

func TestBuildSchemaRewriteSed_DowngradePublicToSharedSchema(t *testing.T) {
	if _, err := exec.LookPath("sed"); err != nil {
		t.Skip("sed not available in test environment")
	}

	src := "public"
	dst := "tnt_acme_order_db"
	input := `CREATE SCHEMA public;
SET search_path = public, pg_catalog;
CREATE TABLE public.orders (id text);
COPY public.orders (id, customer_id) FROM stdin;
1	customer_1
\.

`

	sedProg := buildSchemaRewriteSed(src, dst)
	cmd := exec.Command("sed", sedProg)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sed failed: %v", err)
	}
	got := string(out)

	wantContains := []string{
		"CREATE SCHEMA IF NOT EXISTS tnt_acme_order_db;",
		"SET search_path = tnt_acme_order_db, pg_catalog;",
		"CREATE TABLE tnt_acme_order_db.orders (id text);",
		"COPY tnt_acme_order_db.orders (id, customer_id) FROM stdin;",
	}
	for _, w := range wantContains {
		if !strings.Contains(got, w) {
			t.Errorf("expected output to contain %q, got:\n%s", w, got)
		}
	}
	if strings.Contains(got, "public.") {
		t.Errorf("output still contains source schema references %q:\n%s", "public.", got)
	}
}
