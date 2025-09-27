package tenantdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"order-service/internal/infrastructure/postgres"
	"order-service/internal/registry"
)

const (
	defaultTenantServiceURL = "http://tenant-service:8082"
)

type dsnResponse struct {
	Data struct {
		DSN        string `json:"dsn"`
		SchemaName string `json:"schema_name"`
	} `json:"data"`
}

type Resolver interface {
	GetTenantDB(ctx context.Context, tenantID string) (*sql.DB, string, error)
}

type tenantDBResolver struct {
	registry         *registry.PoolRegistry
	tenantServiceURL string
}

type ResolverParams struct {
	Registry         *registry.PoolRegistry
	TenantServiceURL string // Optional override for testing or custom host
}

func NewResolver(params ResolverParams) Resolver {
	url := params.TenantServiceURL
	if url == "" {
		url = defaultTenantServiceURL
	}
	return &tenantDBResolver{
		registry:         params.Registry,
		tenantServiceURL: url,
	}
}

func (r *tenantDBResolver) GetTenantDB(ctx context.Context, tenantID string) (*sql.DB, string, error) {
	db, schemaName, err := r.registry.GetOrFetch(tenantID, func() (*sql.DB, string, error) {
		return r.fetchAndOpenPool(ctx, tenantID)
	})
	if err != nil {
		return nil, "", fmt.Errorf("tenant db resolver: failed to resolve DB for tenant '%s': %w", tenantID, err)
	}
	return db, schemaName, nil
}

func (r *tenantDBResolver) fetchAndOpenPool(ctx context.Context, tenantID string) (*sql.DB, string, error) {
	url := fmt.Sprintf("%s/internal/tenants/%s/infrastructure/order-service", r.tenantServiceURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to build DSN request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch DSN from tenant-service: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("tenant-service returned status %d for tenant '%s'", resp.StatusCode, tenantID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read DSN response body: %w", err)
	}

	var dsnResp dsnResponse
	if err := json.Unmarshal(body, &dsnResp); err != nil {
		return nil, "", fmt.Errorf("failed to parse DSN response: %w", err)
	}

	if dsnResp.Data.DSN == "" {
		return nil, "", fmt.Errorf("empty DSN returned for tenant '%s'", tenantID)
	}

	schema := dsnResp.Data.SchemaName
	if schema == "" {
		schema = "public"
	}

	client, err := postgres.NewClientFromDSN(dsnResp.Data.DSN)
	if err != nil {
		return nil, "", err
	}

	return client, schema, nil
}
