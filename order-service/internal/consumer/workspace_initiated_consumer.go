package consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"order-service/internal/infrastructure/rabbitmq"
	"order-service/internal/registry"
	"order-service/internal/service"
)

const (
	ExchangeCompanyEvents             = "company.events"
	RoutingKeyWorkspaceInitiated      = "workspace.initiated"
	QueueOrderServiceWorkspaceInitiated = "order_service_workspace_initiated"

	tenantServiceBaseURL = "http://tenant-service:8082"
)

// WorkspaceInitiatedEvent mirrors the payload published by tenant-service.
type WorkspaceInitiatedEvent struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Plan       string `json:"plan"` // "shared" | "dedicated"
	OwnerEmail string `json:"owner_email"`
	OwnerName  string `json:"owner_name"`
}

// infrastructureWriteBack is the payload sent to PATCH /internal/tenants/{id}/infrastructure
type infrastructureWriteBack struct {
	ServiceName string `json:"service_name"`
	DSN         string `json:"dsn"`
	SchemaName  string `json:"schema_name"`
}

// WorkspaceInitiatedConsumer listens for workspace.initiated events and provisions
// the order-service database infrastructure for each new tenant.
type WorkspaceInitiatedConsumer struct {
	client           *rabbitmq.Client
	provisioner      service.ProvisionerService
	registry         *registry.PoolRegistry
	tenantServiceURL string
}

type WorkspaceInitiatedConsumerParams struct {
	Client           *rabbitmq.Client
	Provisioner      service.ProvisionerService
	Registry         *registry.PoolRegistry
	TenantServiceURL string // override for testing; defaults to tenantServiceBaseURL
}

func NewWorkspaceInitiatedConsumer(params WorkspaceInitiatedConsumerParams) (*WorkspaceInitiatedConsumer, error) {
	if err := params.Client.DeclareExchange(ExchangeCompanyEvents, "topic"); err != nil {
		return nil, fmt.Errorf("failed to declare exchange: %w", err)
	}
	if err := params.Client.DeclareAndBindQueue(
		QueueOrderServiceWorkspaceInitiated, ExchangeCompanyEvents, RoutingKeyWorkspaceInitiated,
	); err != nil {
		return nil, fmt.Errorf("failed to bind queue: %w", err)
	}

	tsURL := params.TenantServiceURL
	if tsURL == "" {
		tsURL = tenantServiceBaseURL
	}

	return &WorkspaceInitiatedConsumer{
		client:           params.Client,
		provisioner:      params.Provisioner,
		registry:         params.Registry,
		tenantServiceURL: tsURL,
	}, nil
}

func (c *WorkspaceInitiatedConsumer) Start(ctx context.Context) error {
	msgs, err := c.client.Channel.Consume(
		QueueOrderServiceWorkspaceInitiated,
		"order-service-provisioner",
		false, // manual ack
		false, false, false, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to consume from queue '%s': %w", QueueOrderServiceWorkspaceInitiated, err)
	}

	log.Printf("OrderService: Listening for workspace.initiated events on queue '%s'...", QueueOrderServiceWorkspaceInitiated)

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Printf("WorkspaceInitiatedConsumer: Context cancelled, shutting down.")
				return
			case d, ok := <-msgs:
				if !ok {
					log.Printf("WorkspaceInitiatedConsumer: Message channel closed.")
					return
				}

				var evt WorkspaceInitiatedEvent
				if err := json.Unmarshal(d.Body, &evt); err != nil {
					log.Printf("WorkspaceInitiatedConsumer Error: Bad payload: %v", err)
					d.Nack(false, false) // dead-letter — bad JSON is unrecoverable
					continue
				}

				log.Printf("WorkspaceInitiatedConsumer: Provisioning order DB for tenant='%s' plan='%s'", evt.TenantID, evt.Plan)

				dsn, schemaName, err := c.provision(ctx, evt)
				if err != nil {
					log.Printf("WorkspaceInitiatedConsumer Error: Provisioning failed for tenant='%s': %v", evt.TenantID, err)
					d.Nack(false, true) // requeue for retry
					continue
				}

				// Write-back to tenant-service Control Plane
				if err := c.writeBack(ctx, evt.TenantID, dsn, schemaName); err != nil {
					log.Printf("WorkspaceInitiatedConsumer Error: Write-back failed for tenant='%s': %v", evt.TenantID, err)
					d.Nack(false, true) // requeue — provisioning is idempotent, writeback can retry
					continue
				}

				d.Ack(false)
				log.Printf("WorkspaceInitiatedConsumer: Successfully provisioned tenant='%s'", evt.TenantID)
			}
		}
	}()

	return nil
}

// provision routes to shared or dedicated provisioner based on plan.
// Returns (dsn, schemaName, error).
func (c *WorkspaceInitiatedConsumer) provision(ctx context.Context, evt WorkspaceInitiatedEvent) (dsn, schemaName string, err error) {
	switch evt.Plan {
	case "shared":
		dsn, schemaName, err = c.provisioner.ProvisionShared(ctx, evt.TenantID)
		return
	case "dedicated":
		dsn, err = c.provisioner.ProvisionDedicated(ctx, evt.TenantID)
		schemaName = "public"
		return
	default:
		return "", "", fmt.Errorf("unknown plan '%s'", evt.Plan)
	}
}

// writeBack calls PATCH /internal/tenants/{tenantID}/infrastructure on tenant-service.
func (c *WorkspaceInitiatedConsumer) writeBack(ctx context.Context, tenantID, dsn, schemaName string) error {
	payload := infrastructureWriteBack{
		ServiceName: "order-service",
		DSN:         dsn,
		SchemaName:  schemaName,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal write-back payload: %w", err)
	}

	url := fmt.Sprintf("%s/internal/tenants/%s/infrastructure", c.tenantServiceURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create write-back request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("write-back HTTP call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("write-back returned non-2xx status: %d", resp.StatusCode)
	}
	return nil
}
