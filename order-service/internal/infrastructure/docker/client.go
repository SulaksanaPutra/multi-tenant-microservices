package docker

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	_ "github.com/lib/pq"
)

const (
	postgresImage = "postgres:16-alpine"
	dbUser        = "postgres"
	dbPassword    = "postgres"
	dbName        = "orders"
)

// Client  wraps the Docker SDK client for order-service container provisioning.
type Client struct {
	cli *client.Client
}

func NewDockerClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// CheckOrCreate implements deterministic idempotency for dedicated order DB containers.
//
// If a container named dedicated_order_db_{tenantID} already exists (any state),
// it inspects and returns the mapped host port — handles the retry/crash scenario.
// If it doesn't exist, it creates and starts a fresh Postgres container.
//
// This makes the consumer infinitely retriable without leaking infrastructure.
func (d *Client) CheckOrCreate(ctx context.Context, tenantID string) (hostPort string, err error) {
	containerName := "dedicated_order_db_" + tenantID

	// 1. Check if container already exists (handles crash + retry scenario)
	existingPort, err := d.findContainerPort(ctx, containerName)
	if err != nil {
		return "", fmt.Errorf("failed to check for existing container '%s': %w", containerName, err)
	}
	if existingPort != "" {
		log.Printf("DockerClient: Container '%s' already exists on port %s. Reusing.", containerName, existingPort)
		return existingPort, nil
	}

	// 2. Pull image if not present (best-effort, may already be cached)
	log.Printf("DockerClient: Creating dedicated container '%s'...", containerName)
	if err := d.ensureImage(ctx); err != nil {
		log.Printf("DockerClient Warning: image pull failed (may already exist): %v", err)
	}

	// 3. Create and start the container
	hostPort, err = d.createAndStart(ctx, containerName, tenantID)
	if err != nil {
		return "", fmt.Errorf("failed to create container '%s': %w", containerName, err)
	}

	// 4. Wait for Postgres to be ready
	if err := d.waitForReady(ctx, hostPort); err != nil {
		return "", fmt.Errorf("container '%s' did not become healthy: %w", containerName, err)
	}

	log.Printf("DockerClient: Container '%s' ready on port %s", containerName, hostPort)
	return hostPort, nil
}

// findContainerPort looks for an existing container by name and returns its mapped port.
// Returns ("", nil) if not found.
func (d *Client) findContainerPort(ctx context.Context, containerName string) (string, error) {
	f := filters.NewArgs()
	f.Add("name", containerName)

	containers, err := d.cli.ContainerList(ctx, container.ListOptions{
		All:     true, // include stopped containers
		Filters: f,
	})
	if err != nil {
		return "", fmt.Errorf("ContainerList failed: %w", err)
	}

	portSpec, _ := nat.NewPort("tcp", "5432")
	for _, c := range containers {
		for _, name := range c.Names {
			if name == "/"+containerName {
				inspect, err := d.cli.ContainerInspect(ctx, c.ID)
				if err != nil {
					return "", fmt.Errorf("ContainerInspect failed for '%s': %w", containerName, err)
				}
				// If stopped, restart it
				if !inspect.State.Running {
					log.Printf("DockerClient: Container '%s' is stopped. Restarting...", containerName)
					if err := d.cli.ContainerStart(ctx, c.ID, container.StartOptions{}); err != nil {
						return "", fmt.Errorf("failed to restart container '%s': %w", containerName, err)
					}
				}
				portBindings := inspect.NetworkSettings.Ports[portSpec]
				if len(portBindings) > 0 {
					return portBindings[0].HostPort, nil
				}
			}
		}
	}
	return "", nil
}

func (d *Client) ensureImage(ctx context.Context) error {
	_, err := d.cli.ImagePull(ctx, postgresImage, image.PullOptions{})
	return err
}

func (d *Client) createAndStart(ctx context.Context, containerName, tenantID string) (string, error) {
	portSpec, _ := nat.NewPort("tcp", "5432")
	hostConfig := &container.HostConfig{
		PortBindings: nat.PortMap{
			portSpec: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: ""}}, // "" = auto-assign
		},
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}

	resp, err := d.cli.ContainerCreate(ctx,
		&container.Config{
			Image: postgresImage,
			Env: []string{
				"POSTGRES_USER=" + dbUser,
				"POSTGRES_PASSWORD=" + dbPassword,
				"POSTGRES_DB=" + dbName,
			},
			Labels: map[string]string{
				"broker.managed-by": "order-service",
				"broker.tenant-id":  tenantID,
			},
		},
		hostConfig,
		nil,
		nil,
		containerName,
	)
	if err != nil {
		return "", fmt.Errorf("ContainerCreate failed: %w", err)
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("ContainerStart failed: %w", err)
	}

	inspect, err := d.cli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return "", fmt.Errorf("ContainerInspect after start failed: %w", err)
	}

	portBindings := inspect.NetworkSettings.Ports[portSpec]
	if len(portBindings) == 0 {
		return "", fmt.Errorf("no port binding found for container '%s'", containerName)
	}
	return portBindings[0].HostPort, nil
}

// waitForReady polls the Postgres port until it accepts connections or times out.
func (d *Client) waitForReady(ctx context.Context, hostPort string) error {
	dsn := fmt.Sprintf("host=localhost port=%s user=%s password=%s dbname=%s sslmode=disable",
		hostPort, dbUser, dbPassword, dbName)

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		db, err := sql.Open("postgres", dsn)
		if err == nil {
			if pingErr := db.Ping(); pingErr == nil {
				err := db.Close()
				if err != nil {
					return err
				}
				return nil
			}
			err := db.Close()
			if err != nil {
				return err
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("postgres on port %s did not become ready within 60s", hostPort)
}
