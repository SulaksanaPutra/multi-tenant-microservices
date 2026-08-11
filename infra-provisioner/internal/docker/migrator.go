package docker

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/lib/pq"
)

type SchemaMigrator struct {
	dockerCli *client.Client
}

func NewSchemaMigrator() (*SchemaMigrator, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client for migrator: %w", err)
	}
	return &SchemaMigrator{dockerCli: cli}, nil
}

// CheckSchemaExists checks if a schema exists in the shared database.
func (m *SchemaMigrator) CheckSchemaExists(ctx context.Context, sharedDSN, schemaName string) (bool, error) {
	db, err := sql.Open("postgres", sharedDSN)
	if err != nil {
		return false, fmt.Errorf("failed to open shared db for schema check: %w", err)
	}
	defer func() { _ = db.Close() }()

	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1);"
	if err := db.QueryRowContext(ctx, query, schemaName).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check schema existence: %w", err)
	}
	return exists, nil
}

// LockSchema renames schemaName to lockedSchemaName with a 15s lock timeout.
// Returns error (e.g. timeout error) if active transactions block the lock.
func (m *SchemaMigrator) LockSchema(ctx context.Context, sharedDSN, schemaName, lockedSchemaName string) error {
	if err := ValidateIdentifier(schemaName); err != nil {
		return err
	}
	if err := ValidateIdentifier(lockedSchemaName); err != nil {
		return err
	}

	db, err := sql.Open("postgres", sharedDSN)
	if err != nil {
		return fmt.Errorf("failed to open shared db for schema lock: %w", err)
	}
	defer func() { _ = db.Close() }()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin schema lock transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "SET lock_timeout = '15s';"); err != nil {
		return fmt.Errorf("failed to set lock timeout: %w", err)
	}

	alterQuery := fmt.Sprintf("ALTER SCHEMA %s RENAME TO %s;",
		pq.QuoteIdentifier(schemaName), pq.QuoteIdentifier(lockedSchemaName))

	if _, err := tx.ExecContext(ctx, alterQuery); err != nil {
		return fmt.Errorf("failed to rename schema '%s' to '%s' (lock timeout or active tx): %w", schemaName, lockedSchemaName, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit schema rename transaction: %w", err)
	}

	log.Printf("SchemaMigrator: Locked schema '%s' by renaming to '%s'", schemaName, lockedSchemaName)
	return nil
}

// RestoreSchema renames lockedSchemaName back to originalSchemaName on rollback.
func (m *SchemaMigrator) RestoreSchema(ctx context.Context, sharedDSN, lockedSchemaName, originalSchemaName string) error {
	if err := ValidateIdentifier(lockedSchemaName); err != nil {
		return err
	}
	if err := ValidateIdentifier(originalSchemaName); err != nil {
		return err
	}

	db, err := sql.Open("postgres", sharedDSN)
	if err != nil {
		return fmt.Errorf("failed to open shared db for schema restore: %w", err)
	}
	defer func() { _ = db.Close() }()

	alterQuery := fmt.Sprintf("ALTER SCHEMA %s RENAME TO %s;",
		pq.QuoteIdentifier(lockedSchemaName), pq.QuoteIdentifier(originalSchemaName))

	if _, err := db.ExecContext(ctx, alterQuery); err != nil {
		return fmt.Errorf("failed to restore schema name from '%s' to '%s': %w", lockedSchemaName, originalSchemaName, err)
	}

	log.Printf("SchemaMigrator: Restored schema name from '%s' back to '%s'", lockedSchemaName, originalSchemaName)
	return nil
}

// MigrateData pipes pg_dump of sourceSchema (in the source database) into psql on the
// target database, using sed to rewrite the schema to targetSchema on the fly.
// The target schema's orders/outbox tables are dropped first so re-runs under
// RabbitMQ at-least-once delivery are idempotent.
func (m *SchemaMigrator) MigrateData(
	ctx context.Context,
	sourceHost string, sourcePort int, sourceUser, sourcePass, sourceDB, sourceSchema string,
	targetHost string, targetPort int, targetUser, targetPass, targetDB, targetSchema string,
) error {
	if err := ValidateIdentifier(sourceSchema); err != nil {
		return fmt.Errorf("invalid source schema '%s': %w", sourceSchema, err)
	}
	if err := ValidateIdentifier(targetSchema); err != nil {
		return fmt.Errorf("invalid target schema '%s': %w", targetSchema, err)
	}

	// 1. Pre-clean target tables so a redelivered migration re-applies cleanly.
	if err := m.cleanTargetTables(ctx, targetHost, targetPort, targetUser, targetPass, targetDB, targetSchema); err != nil {
		return err
	}

	// 2. Require the PostgreSQL client tooling; a missing binary is a hard error,
	// never a silent no-op (otherwise tenants cut over with empty databases).
	pgDumpPath, err := exec.LookPath("pg_dump")
	if err != nil {
		return fmt.Errorf("pg_dump not found in PATH: %w", err)
	}
	psqlPath, err := exec.LookPath("psql")
	if err != nil {
		return fmt.Errorf("psql not found in PATH: %w", err)
	}
	sedPath, err := exec.LookPath("sed")
	if err != nil {
		return fmt.Errorf("sed not found in PATH: %w", err)
	}

	// 3. Pipe: pg_dump | sed | psql
	dumpCmd := exec.CommandContext(ctx, pgDumpPath,
		"-h", sourceHost,
		"-p", fmt.Sprintf("%d", sourcePort),
		"-U", sourceUser,
		"-d", sourceDB,
		"-n", sourceSchema,
		"--no-owner",
		"--no-acl",
	)
	dumpCmd.Env = append(os.Environ(), "PGPASSWORD="+sourcePass)

	sedCmd := exec.CommandContext(ctx, sedPath, buildSchemaRewriteSed(sourceSchema, targetSchema))

	psqlCmd := exec.CommandContext(ctx, psqlPath,
		"-h", targetHost,
		"-p", fmt.Sprintf("%d", targetPort),
		"-U", targetUser,
		"-d", targetDB,
		"-v", "ON_ERROR_STOP=1",
	)
	psqlCmd.Env = append(os.Environ(), "PGPASSWORD="+targetPass)

	dumpOut, err := dumpCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create dump stdout pipe: %w", err)
	}
	sedCmd.Stdin = dumpOut

	sedOut, err := sedCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create sed stdout pipe: %w", err)
	}
	psqlCmd.Stdin = sedOut

	var errBuf bytes.Buffer
	dumpCmd.Stderr = &errBuf
	sedCmd.Stderr = &errBuf
	psqlCmd.Stderr = &errBuf

	if err := dumpCmd.Start(); err != nil {
		return fmt.Errorf("failed to start pg_dump: %w", err)
	}
	if err := sedCmd.Start(); err != nil {
		return fmt.Errorf("failed to start sed: %w", err)
	}
	if err := psqlCmd.Start(); err != nil {
		return fmt.Errorf("failed to start psql: %w", err)
	}

	if err := dumpCmd.Wait(); err != nil {
		return fmt.Errorf("pg_dump failed: %w (stderr: %s)", err, errBuf.String())
	}
	if err := sedCmd.Wait(); err != nil {
		return fmt.Errorf("sed failed: %w (stderr: %s)", err, errBuf.String())
	}
	if err := psqlCmd.Wait(); err != nil {
		return fmt.Errorf("psql failed: %w (stderr: %s)", err, errBuf.String())
	}

	log.Printf("SchemaMigrator: Data successfully migrated from schema '%s' to '%s' on %s:%d/%s",
		sourceSchema, targetSchema, targetHost, targetPort, targetDB)
	return nil
}

// buildSchemaRewriteSed produces a sed program rewriting every occurrence of
// srcSchema to dstSchema in a pg_dump script: the CREATE SCHEMA statement, the
// SET search_path clause, and every schema-qualified object reference.
func buildSchemaRewriteSed(srcSchema, dstSchema string) string {
	q := pq.QuoteIdentifier(srcSchema)
	qd := pq.QuoteIdentifier(dstSchema)
	return fmt.Sprintf(
		"s/CREATE SCHEMA %s;/CREATE SCHEMA IF NOT EXISTS %s;/g;"+
			"s/CREATE SCHEMA %s;/CREATE SCHEMA IF NOT EXISTS %s;/g;"+
			"s/SET search_path = %s/SET search_path = %s/g;"+
			"s/SET search_path = %s/SET search_path = %s/g;"+
			"s/%s\\./%s./g",
		q, qd,
		srcSchema, dstSchema,
		q, qd,
		srcSchema, dstSchema,
		srcSchema, dstSchema,
	)
}

// cleanTargetTables drops the orders/outbox tables in the target schema so a
// migration re-run starts from an empty target rather than failing or duplicating rows.
func (m *SchemaMigrator) cleanTargetTables(ctx context.Context, host string, port int, user, pass, db, schema string) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, pass, db)

	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open target db for pre-clean: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping target db for pre-clean: %w", err)
	}

	q := pq.QuoteIdentifier(schema)
	dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s.orders; DROP TABLE IF EXISTS %s.outbox;", q, q)
	if _, err := conn.ExecContext(ctx, dropSQL); err != nil {
		return fmt.Errorf("failed to clean target tables in schema '%s': %w", schema, err)
	}
	return nil
}

// TableHasRows reports whether the given table exists in the given schema and contains at least one row.
func (m *SchemaMigrator) TableHasRows(ctx context.Context, dsn, schema, table string) (bool, error) {
	if err := ValidateIdentifier(schema); err != nil {
		return false, fmt.Errorf("invalid schema '%s': %w", schema, err)
	}
	if err := ValidateIdentifier(table); err != nil {
		return false, fmt.Errorf("invalid table '%s': %w", table, err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return false, fmt.Errorf("failed to open db for table check: %w", err)
	}
	defer func() { _ = db.Close() }()

	query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s.%s LIMIT 1);",
		pq.QuoteIdentifier(schema), pq.QuoteIdentifier(table))

	var hasRows bool
	if err := db.QueryRowContext(ctx, query).Scan(&hasRows); err != nil {
		return false, err
	}
	return hasRows, nil
}

// DropSchemaIfExists drops a schema and all its objects, used to clean up the
// leftover "_locked" schema after a successful cutover.
func (m *SchemaMigrator) DropSchemaIfExists(ctx context.Context, dsn, schemaName string) error {
	if err := ValidateIdentifier(schemaName); err != nil {
		return err
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open db for schema drop: %w", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE;", pq.QuoteIdentifier(schemaName))); err != nil {
		return fmt.Errorf("failed to drop schema '%s': %w", schemaName, err)
	}

	log.Printf("SchemaMigrator: Dropped schema '%s'", schemaName)
	return nil
}

// provisionStatements holds the SQL produced for a same-instance tenant
// database provisioning, split by which database each statement runs against.
type provisionStatements struct {
	maintenance []string // executed on the maintenance (postgres) database
	target      []string // executed on the tenant's own database
}

// provisionTenantDatabaseStatements builds the DDL/grants for creating a
// per-tenant role and database. The existence flags let callers pick CREATE vs
// ALTER for the role and skip the database creation when it already exists.
func provisionTenantDatabaseStatements(dbName, roleName, rolePass string, roleExists, dbExists bool) (provisionStatements, error) {
	if err := ValidateIdentifier(dbName); err != nil {
		return provisionStatements{}, fmt.Errorf("invalid database name '%s': %w", dbName, err)
	}
	if err := ValidateIdentifier(roleName); err != nil {
		return provisionStatements{}, fmt.Errorf("invalid role name '%s': %w", roleName, err)
	}

	q := pq.QuoteIdentifier(dbName)
	r := pq.QuoteIdentifier(roleName)

	maintenance := []string{}
	if roleExists {
		maintenance = append(maintenance, fmt.Sprintf("ALTER ROLE %s WITH PASSWORD %s;", r, pq.QuoteLiteral(rolePass)))
	} else {
		maintenance = append(maintenance, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD %s;", r, pq.QuoteLiteral(rolePass)))
	}
	if !dbExists {
		maintenance = append(maintenance, fmt.Sprintf("CREATE DATABASE %s OWNER %s;", q, r))
	}
	maintenance = append(maintenance,
		fmt.Sprintf("REVOKE CONNECT ON DATABASE %s FROM PUBLIC;", q),
		fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s;", q, r),
	)

	return provisionStatements{
		maintenance: maintenance,
		target:      []string{fmt.Sprintf("GRANT ALL ON SCHEMA public TO %s;", r)},
	}, nil
}

// dropTenantDatabaseStatements builds the DDL that removes a per-tenant database
// and role. DROP OWNED is only emitted when the role still exists.
func dropTenantDatabaseStatements(dbName, roleName string, roleExists bool) ([]string, error) {
	if err := ValidateIdentifier(dbName); err != nil {
		return nil, fmt.Errorf("invalid database name '%s': %w", dbName, err)
	}
	if err := ValidateIdentifier(roleName); err != nil {
		return nil, fmt.Errorf("invalid role name '%s': %w", roleName, err)
	}

	q := pq.QuoteIdentifier(dbName)
	r := pq.QuoteIdentifier(roleName)

	stmts := []string{fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE);", q)}
	if roleExists {
		stmts = append(stmts, fmt.Sprintf("DROP OWNED BY %s;", r))
	}
	stmts = append(stmts, fmt.Sprintf("DROP ROLE IF EXISTS %s;", r))
	return stmts, nil
}

// ProvisionTenantDatabase creates a per-tenant database and role inside the
// shared PostgreSQL instance (same_instance isolation mode). The role is the
// database owner and PUBLIC connect is revoked so tenants cannot reach each
// other's databases.
func (m *SchemaMigrator) ProvisionTenantDatabase(ctx context.Context, host string, port int, superUser, superPass, dbName, roleName, rolePass string) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=postgres sslmode=disable",
		host, port, superUser, superPass)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open maintenance db: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping maintenance db: %w", err)
	}

	var roleExists bool
	_ = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1);", roleName).Scan(&roleExists)
	var dbExists bool
	_ = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1);", dbName).Scan(&dbExists)

	stmts, err := provisionTenantDatabaseStatements(dbName, roleName, rolePass, roleExists, dbExists)
	if err != nil {
		return err
	}

	for _, stmt := range stmts.maintenance {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to execute %s: %w", stmt, err)
		}
	}

	// The schema grant must run against the tenant's own database.
	domainDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, superUser, superPass, dbName)
	domainDB, dErr := sql.Open("postgres", domainDSN)
	if dErr == nil {
		for _, stmt := range stmts.target {
			_, _ = domainDB.ExecContext(ctx, stmt)
		}
		_ = domainDB.Close()
	}

	log.Printf("SchemaMigrator: Provisioned tenant database '%s' (owner role '%s')", dbName, roleName)
	return nil
}

// DropTenantDatabase removes a per-tenant database and role inside the shared
// PostgreSQL instance (same_instance isolation mode). Used to purge the
// dedicated database on downgrade or rollback.
func (m *SchemaMigrator) DropTenantDatabase(ctx context.Context, host string, port int, superUser, superPass, dbName, roleName string) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=postgres sslmode=disable",
		host, port, superUser, superPass)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open maintenance db: %w", err)
	}
	defer func() { _ = db.Close() }()

	var roleExists bool
	_ = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1);", roleName).Scan(&roleExists)

	stmts, err := dropTenantDatabaseStatements(dbName, roleName, roleExists)
	if err != nil {
		return err
	}

	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to execute %s: %w", stmt, err)
		}
	}

	log.Printf("SchemaMigrator: Dropped tenant database '%s' (role '%s')", dbName, roleName)
	return nil
}

// DestroyContainer force-removes a docker container on migration rollback.
func (m *SchemaMigrator) DestroyContainer(ctx context.Context, containerName string) error {
	if m.dockerCli == nil {
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := m.dockerCli.ContainerRemove(timeoutCtx, containerName, container.RemoveOptions{
		Force: true,
	})
	if err != nil {
		log.Printf("SchemaMigrator Warning: Failed to force remove container '%s': %v", containerName, err)
		return err
	}
	log.Printf("SchemaMigrator: Container '%s' force-removed during compensating rollback", containerName)
	return nil
}
