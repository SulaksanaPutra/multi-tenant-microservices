package docker

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/lib/pq"

	"infra-provisioner/internal/crypto"
)

var validIdentifierRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// ValidateIdentifier enforces strict alphanumeric & underscore validation to prevent SQL injection in DDL queries.
func ValidateIdentifier(name string) error {
	if !validIdentifierRegex.MatchString(name) {
		return fmt.Errorf("invalid identifier '%s': must contain strictly alphanumeric characters and underscores", name)
	}
	return nil
}

type Provisioner interface {
	ProvisionDedicatedContainer(ctx context.Context, tenantID, infraMasterSecret string, domainSecrets map[string]string) (host string, port int, dbName, dbUser string, err error)
}

type DockerProvisioner struct {
	cli         *client.Client
	networkName string
}

func NewDockerProvisioner(networkName string) (*DockerProvisioner, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	if networkName == "" {
		networkName = "microservice-network"
	}

	return &DockerProvisioner{
		cli:         cli,
		networkName: networkName,
	}, nil
}

func (p *DockerProvisioner) ProvisionDedicatedContainer(
	ctx context.Context,
	tenantID string,
	infraMasterSecret string,
	domainSecrets map[string]string,
) (host string, port int, dbName, dbUser string, err error) {
	sanitizedID := sanitizeTenantID(tenantID)
	if err := ValidateIdentifier(sanitizedID); err != nil {
		return "", 0, "", "", fmt.Errorf("tenantID validation failed: %w", err)
	}

	containerName := fmt.Sprintf("postgres-tenant-%s", sanitizedID)
	rootUser := "postgres"
	rootDBName := "postgres"
	port = 5432
	host = containerName

	// 1. Root container password derived from INFRA_MASTER_SECRET (Only infra-provisioner possesses this)
	rootPassword := crypto.DeriveTenantDBPassword(infraMasterSecret, tenantID)

	// 2. Check if container already exists
	inspect, err := p.cli.ContainerInspect(ctx, containerName)
	if err == nil {
		log.Printf("DockerProvisioner: Dedicated container '%s' already exists (State: %s).", containerName, inspect.State.Status)
		if !inspect.State.Running {
			log.Printf("DockerProvisioner: Starting stopped container '%s'...", containerName)
			if err := p.cli.ContainerStart(ctx, inspect.ID, types.ContainerStartOptions{}); err != nil {
				return "", 0, "", "", fmt.Errorf("failed to start existing container '%s': %w", containerName, err)
			}
		}
	} else {
		// Container does not exist — create it with strict host resource bounds
		log.Printf("DockerProvisioner: Creating dedicated container '%s' with 512MB RAM & 0.5 CPU limits...", containerName)

		config := &container.Config{
			Image: "postgres:16-alpine",
			Env: []string{
				"POSTGRES_USER=" + rootUser,
				"POSTGRES_PASSWORD=" + rootPassword,
				"POSTGRES_DB=" + rootDBName,
			},
		}

		hostConfig := &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: "always"},
			Resources: container.Resources{
				Memory:   512 * 1024 * 1024, // 512MB hard limit
				NanoCPUs: 500000000,         // 0.5 CPU cores
			},
		}

		netConfig := &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				p.networkName: {},
			},
		}

		created, err := p.cli.ContainerCreate(ctx, config, hostConfig, netConfig, nil, containerName)
		if err != nil {
			return "", 0, "", "", fmt.Errorf("failed to create container '%s': %w", containerName, err)
		}

		log.Printf("DockerProvisioner: Container '%s' created (ID: %s). Starting...", containerName, created.ID[:12])
		if err := p.cli.ContainerStart(ctx, created.ID, types.ContainerStartOptions{}); err != nil {
			return "", 0, "", "", fmt.Errorf("failed to start container '%s': %w", containerName, err)
		}
	}

	// 3. Health check: Wait until PostgreSQL inside container is accepting connections
	if err := p.waitForPostgresReady(ctx, host, port, rootUser, rootPassword, rootDBName); err != nil {
		return "", 0, "", "", fmt.Errorf("container '%s' failed health check: %w", containerName, err)
	}

	// 4. Configuration-Driven Domain Bootstrapping
	// Connect as root superuser and generically bootstrap domain databases & isolated roles
	if err := p.bootstrapDomainDatabases(ctx, host, port, rootUser, rootPassword, rootDBName, tenantID, domainSecrets); err != nil {
		return "", 0, "", "", fmt.Errorf("failed to bootstrap domain databases for '%s': %w", containerName, err)
	}

	log.Printf("DockerProvisioner: Container '%s' is HEALTHY with domain roles bootstrapped.", containerName)
	return host, port, "order_db", "order_user", nil
}

func (p *DockerProvisioner) bootstrapDomainDatabases(
	ctx context.Context,
	host string,
	port int,
	rootUser, rootPassword, rootDBName string,
	tenantID string,
	domainSecrets map[string]string,
) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, rootUser, rootPassword, rootDBName)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open root db connection: %w", err)
	}
	defer db.Close()

	for domainDB, domainSecret := range domainSecrets {
		if err := ValidateIdentifier(domainDB); err != nil {
			return fmt.Errorf("domainDB identifier validation failed for '%s': %w", domainDB, err)
		}

		user := fmt.Sprintf("%s_user", strings.TrimSuffix(domainDB, "_db"))
		if err := ValidateIdentifier(user); err != nil {
			return fmt.Errorf("user identifier validation failed for '%s': %w", user, err)
		}

		pass := crypto.DeriveTenantDBPassword(domainSecret, tenantID)

		log.Printf("DockerProvisioner: Bootstrapping domain database '%s' and role '%s'...", domainDB, user)

		quotedDB := pq.QuoteIdentifier(domainDB)
		quotedUser := pq.QuoteIdentifier(user)

		// Create database if missing
		var dbExists bool
		_ = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1);", domainDB).Scan(&dbExists)
		if !dbExists {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s;", quotedDB)); err != nil {
				return fmt.Errorf("failed to create database %s: %w", quotedDB, err)
			}
		}

		// Create or update role password
		var roleExists bool
		_ = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1);", user).Scan(&roleExists)
		if !roleExists {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE USER %s WITH PASSWORD %s;", quotedUser, pq.QuoteLiteral(pass))); err != nil {
				return fmt.Errorf("failed to create role %s: %w", quotedUser, err)
			}
		} else {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER USER %s WITH PASSWORD %s;", quotedUser, pq.QuoteLiteral(pass))); err != nil {
				return fmt.Errorf("failed to update password for role %s: %w", quotedUser, err)
			}
		}

		// Grant privileges to domain user
		_, _ = db.ExecContext(ctx, fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s;", quotedDB, quotedUser))

		// Grant schema public privileges in domain database
		domainDSN := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
			host, port, rootUser, rootPassword, domainDB)
		domainDBConn, dErr := sql.Open("postgres", domainDSN)
		if dErr == nil {
			_, _ = domainDBConn.ExecContext(ctx, fmt.Sprintf("GRANT ALL ON SCHEMA public TO %s;", quotedUser))
			_ = domainDBConn.Close()
		}
	}

	return nil
}

func (p *DockerProvisioner) waitForPostgresReady(ctx context.Context, host string, port int, user, password, dbName string) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, dbName)

	log.Printf("DockerProvisioner: Waiting for PostgreSQL at %s:%d to accept connections...", host, port)

	for i := 0; i < 15; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			db, err := sql.Open("postgres", dsn)
			if err == nil {
				pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err = db.PingContext(pingCtx)
				cancel()
				_ = db.Close()

				if err == nil {
					log.Printf("DockerProvisioner: PostgreSQL at %s:%d ping successful!", host, port)
					return nil
				}
			}
			log.Printf("DockerProvisioner: Postgres at %s:%d not ready yet (attempt %d/15). Retrying in 2s...", host, port, i+1)
			time.Sleep(2 * time.Second)
		}
	}

	return fmt.Errorf("timed out waiting for postgres at %s:%d after 30s", host, port)
}

func sanitizeTenantID(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), "-", "_")
}
