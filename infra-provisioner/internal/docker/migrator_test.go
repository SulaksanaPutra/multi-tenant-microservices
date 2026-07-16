package docker

import (
	"os/exec"
	"strings"
	"testing"
)

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
