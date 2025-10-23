package tenantdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"order-service/internal/crypto"
	"order-service/internal/infrastructure/postgres"
	"order-service/internal/registry"
)

const (
	defaultTenantServiceURL = "http://tenant-service:8082"
)

type routingResponse struct {
	Data struct {
		DBHost     string `json:"db_host"`
		DBPort     int    `json:"db_port"`
		DBName     string `json:"db_name"`
		DBUser     string `json:"db_user"`
		SchemaName string `json:"schema_name"`
	} `json:"data"`
}

type Resolver interface {
	GetTenantDB(ctx context.Context, tenantID string) (*sql.DB, string, error)
}

type tenantDBResolver struct {
	poolRegistry         *registry.PoolRegistry
	routingRegistry      *registry.RoutingRegistry
	tenantServiceURL     string
	internalServiceToken string
	sharedSecret         string
	sharedDBPass         string
}

type ResolverParams struct {
	PoolRegistry         *registry.PoolRegistry
	RoutingRegistry      *registry.RoutingRegistry
	TenantServiceURL     string
	InternalServiceToken string
	SharedSecret         string
	SharedDBPass         string
}

func NewResolver(params ResolverParams) Resolver {
	url := params.TenantServiceURL
	if url == "" {
		url = defaultTenantServiceURL
	}
	token := params.InternalServiceToken
	if token == "" {
		token = "default_internal_service_token"
	}
	pass := params.SharedDBPass
	if pass == "" {
		pass = "postgres"
	}

	return &tenantDBResolver{
		poolRegistry:         params.PoolRegistry,
		routingRegistry:      params.RoutingRegistry,
		tenantServiceURL:     url,
		internalServiceToken: token,
		sharedSecret:         params.SharedSecret,
		sharedDBPass:         pass,
	}
}

func (r *tenantDBResolver) GetTenantDB(ctx context.Context, tenantID string) (*sql.DB, string, error) {
	db, schemaName, err := r.poolRegistry.GetOrFetch(tenantID, func() (*sql.DB, string, error) {
		return r.resolveAndOpenPool(ctx, tenantID)
	})
	if err != nil {
		return nil, "", fmt.Errorf("tenant db resolver: failed to resolve DB for tenant '%s': %w", tenantID, err)
	}
	return db, schemaName, nil
}

func (r *tenantDBResolver) resolveAndOpenPool(ctx context.Context, tenantID string) (*sql.DB, string, error) {
	// Fast Path: Check local RoutingRegistry materialized view (0ms RAM lookup, 0 network calls)
	if meta, ok := r.routingRegistry.Get(tenantID); ok {
		return r.openPoolFromMetadata(tenantID, meta)
	}

	// Slow Path / Fallback: Fetch metadata from tenant-service via HTTP
	meta, err := r.fetchRoutingFromService(ctx, tenantID)
	if err != nil {
		return nil, "", err
	}

	// Store in local RoutingRegistry for future connection resets / pool re-creations
	r.routingRegistry.Set(meta)

	return r.openPoolFromMetadata(tenantID, meta)
}

func (r *tenantDBResolver) fetchRoutingFromService(ctx context.Context, tenantID string) (registry.RoutingMetadata, error) {
	url := fmt.Sprintf("%s/internal/tenants/%s/infrastructure/order-service", r.tenantServiceURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return registry.RoutingMetadata{}, fmt.Errorf("failed to build routing request: %w", err)
	}

	// Zero-Trust inter-service authorization header
	req.Header.Set("X-Internal-Service-Token", r.internalServiceToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return registry.RoutingMetadata{}, fmt.Errorf("failed to fetch routing metadata from tenant-service: %w", err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return registry.RoutingMetadata{}, fmt.Errorf("tenant-service returned status %d for tenant '%s'", resp.StatusCode, tenantID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return registry.RoutingMetadata{}, fmt.Errorf("failed to read routing response body: %w", err)
	}

	var res routingResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return registry.RoutingMetadata{}, fmt.Errorf("failed to parse routing response JSON: %w", err)
	}

	meta := res.Data
	if meta.DBHost == "" {
		return registry.RoutingMetadata{}, fmt.Errorf("empty db_host returned for tenant '%s'", tenantID)
	}

	return registry.RoutingMetadata{
		TenantID:   tenantID,
		DBHost:     meta.DBHost,
		DBPort:     meta.DBPort,
		DBName:     meta.DBName,
		DBUser:     meta.DBUser,
		SchemaName: meta.SchemaName,
	}, nil
}

func (r *tenantDBResolver) openPoolFromMetadata(tenantID string, meta registry.RoutingMetadata) (*sql.DB, string, error) {
	// Statelessly derive database password in memory
	var pass string
	if meta.DBHost == "postgres" || meta.DBHost == "localhost" {
		pass = r.sharedDBPass
	} else {
		pass = crypto.DeriveTenantDBPassword(r.sharedSecret, tenantID)
	}

	port := meta.DBPort
	if port <= 0 {
		port = 5432
	}
	user := meta.DBUser
	if user == "" {
		user = "postgres"
	}
	schema := meta.SchemaName
	if schema == "" {
		schema = "public"
	}

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		meta.DBHost, port, user, pass, meta.DBName)

	client, err := postgres.NewClientFromDSN(dsn)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open database pool for host '%s': %w", meta.DBHost, err)
	}

	return client, schema, nil
}
