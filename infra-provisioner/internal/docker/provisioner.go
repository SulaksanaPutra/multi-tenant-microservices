package docker

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	_ "github.com/lib/pq"

	"infra-provisioner/internal/crypto"
)

type Provisioner interface {
	ProvisionDedicatedContainer(ctx context.Context, tenantID, sharedSecret string) (host string, port int, dbName, dbUser string, err error)
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

func (p *DockerProvisioner) ProvisionDedicatedContainer(ctx context.Context, tenantID, sharedSecret string) (host string, port int, dbName, dbUser string, err error) {
	sanitizedID := sanitizeTenantID(tenantID)
	containerName := fmt.Sprintf("postgres-tenant-%s", sanitizedID)
	dbUser = "postgres"
	dbName = "postgres"
	port = 5432
	host = containerName

	password := crypto.DeriveTenantDBPassword(sharedSecret, tenantID)

	// 1. Check if container already exists
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
				"POSTGRES_USER=" + dbUser,
				"POSTGRES_PASSWORD=" + password,
				"POSTGRES_DB=" + dbName,
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

	// 2. Health check: Wait until PostgreSQL inside container is accepting connections
	if err := p.waitForPostgresReady(ctx, host, port, dbUser, password, dbName); err != nil {
		return "", 0, "", "", fmt.Errorf("container '%s' failed health check: %w", containerName, err)
	}

	log.Printf("DockerProvisioner: Container '%s' is HEALTHY and ready for connections.", containerName)
	return host, port, dbName, dbUser, nil
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
