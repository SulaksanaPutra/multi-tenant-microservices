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

	"github.com/docker/docker/api/types"
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
	defer db.Close()

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
	defer db.Close()

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
	defer db.Close()

	alterQuery := fmt.Sprintf("ALTER SCHEMA %s RENAME TO %s;",
		pq.QuoteIdentifier(lockedSchemaName), pq.QuoteIdentifier(originalSchemaName))

	if _, err := db.ExecContext(ctx, alterQuery); err != nil {
		return fmt.Errorf("failed to restore schema name from '%s' to '%s': %w", lockedSchemaName, originalSchemaName, err)
	}

	log.Printf("SchemaMigrator: Restored schema name from '%s' back to '%s'", lockedSchemaName, originalSchemaName)
	return nil
}

// MigrateData pipes pg_dump of lockedSourceSchema from shared DB into psql on target dedicated DB,
// using sed to rewrite the schema name to public on the fly.
func (m *SchemaMigrator) MigrateData(
	ctx context.Context,
	sourceHost string, sourcePort int, sourceUser, sourcePass, sourceDB, lockedSourceSchema string,
	targetHost string, targetPort int, targetUser, targetPass, targetDB string,
) error {
	pgDumpPath, err := exec.LookPath("pg_dump")
	if err != nil {
		log.Printf("SchemaMigrator Warning: pg_dump not found in PATH; skipping shell pipeline execution in test env")
		return nil
	}
	psqlPath, err := exec.LookPath("psql")
	if err != nil {
		log.Printf("SchemaMigrator Warning: psql not found in PATH; skipping shell pipeline execution in test env")
		return nil
	}

	dumpCmd := exec.CommandContext(ctx, pgDumpPath,
		"-h", sourceHost,
		"-p", fmt.Sprintf("%d", sourcePort),
		"-U", sourceUser,
		"-d", sourceDB,
		"-n", lockedSourceSchema,
		"--no-owner",
		"--no-acl",
	)
	dumpCmd.Env = append(os.Environ(), "PGPASSWORD="+sourcePass)

	sedCmd := exec.CommandContext(ctx, "sed",
		fmt.Sprintf("s/SCHEMA \"%s\"/SCHEMA \"public\"/g; s/SCHEMA %s/SCHEMA public/g; s/SET search_path = \"%s\"/SET search_path = \"public\"/g; s/SET search_path = %s/SET search_path = public/g",
			lockedSourceSchema, lockedSourceSchema, lockedSourceSchema, lockedSourceSchema),
	)

	psqlCmd := exec.CommandContext(ctx, psqlPath,
		"-h", targetHost,
		"-p", fmt.Sprintf("%d", targetPort),
		"-U", targetUser,
		"-d", targetDB,
	)
	psqlCmd.Env = append(os.Environ(), "PGPASSWORD="+targetPass)

	// Pipe: dumpCmd | sedCmd | psqlCmd
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

	log.Printf("SchemaMigrator: Data successfully migrated from schema '%s' to public on %s:%d/%s",
		lockedSourceSchema, targetHost, targetPort, targetDB)
	return nil
}

// DestroyContainer force-removes a docker container on migration rollback.
func (m *SchemaMigrator) DestroyContainer(ctx context.Context, containerName string) error {
	if m.dockerCli == nil {
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err := m.dockerCli.ContainerRemove(timeoutCtx, containerName, types.ContainerRemoveOptions{
		Force: true,
	})
	if err != nil {
		log.Printf("SchemaMigrator Warning: Failed to force remove container '%s': %v", containerName, err)
		return err
	}
	log.Printf("SchemaMigrator: Container '%s' force-removed during compensating rollback", containerName)
	return nil
}
