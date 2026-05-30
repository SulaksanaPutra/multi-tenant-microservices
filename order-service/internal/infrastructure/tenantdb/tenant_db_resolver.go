package tenantdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

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

type Resolver struct {
	poolRegistry         *registry.PoolRegistry
	routingRegistry      *registry.RoutingRegistry
	tenantServiceURL     string
	internalServiceToken string
	sharedSecret         string
	sharedDBPass         string

	sharedPoolMu sync.Mutex
	sharedPool   *sql.DB

	// httpSemaphore caps concurrent outbound routing fetches to tenant-service.
	// When all 50 tokens are held, new callers are rejected immediately (fail-fast)
	// rather than blocking a Gin HTTP worker goroutine and risking worker-pool starvation.
	httpSemaphore chan struct{}
}

type Params struct {
	PoolRegistry         *registry.PoolRegistry
	RoutingRegistry      *registry.RoutingRegistry
	TenantServiceURL     string
	InternalServiceToken string
	SharedSecret         string
	SharedDBPass         string
}

func NewResolver(params Params) *Resolver {
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

	return &Resolver{
		poolRegistry:         params.PoolRegistry,
		routingRegistry:      params.RoutingRegistry,
		tenantServiceURL:     url,
		internalServiceToken: token,
		sharedSecret:         params.SharedSecret,
		sharedDBPass:         pass,
		// 50 concurrent control-plane fetches is a generous ceiling that protects
		// tenant-service's PostgreSQL connection pool while still absorbing normal
		// burst traffic after a cache purge.
		httpSemaphore: make(chan struct{}, 50),
	}
}

func (r *Resolver) GetTenantDB(ctx context.Context, tenantID string) (Config, error) {
	// 1. Fast Path: Check local RoutingRegistry materialized view
	meta, ok := r.routingRegistry.Get(tenantID)
	if !ok {
		// 2. Slow Path: Fetch routing metadata from tenant-service via HTTP
		var err error
		meta, err = r.fetchRoutingFromService(ctx, tenantID)
		if err != nil {
			return Config{}, fmt.Errorf("tenant db resolver: failed to resolve DB for tenant '%s': %w", tenantID, err)
		}
		r.routingRegistry.Set(meta)
	}

	if meta.Status == "MIGRATING" {
		return Config{}, ErrTenantMigrating
	}

	// 3. Shared Plan Duality Check:
	// If DBHost is the shared Postgres cluster ("postgres" or "localhost"), return the single shared pool.
	if isSharedHost(meta.DBHost) {
		pool, err := r.getSharedPool(meta)
		if err != nil {
			return Config{}, fmt.Errorf("tenant db resolver: failed to get shared pool for tenant '%s': %w", tenantID, err)
		}
		return Config{
			TenantID:   tenantID,
			DB:         pool,
			SchemaName: meta.SchemaName,
		}, nil
	}

	// 4. Dedicated Plan: Route through Bounded LRU PoolRegistry
	db, schemaName, err := r.poolRegistry.GetOrFetch(tenantID, func() (*sql.DB, string, error) {
		return r.openDedicatedPool(tenantID, meta)
	})
	if err != nil {
		return Config{}, fmt.Errorf("tenant db resolver: failed to open dedicated pool for tenant '%s': %w", tenantID, err)
	}

	return Config{
		TenantID:   tenantID,
		DB:         db,
		SchemaName: schemaName,
	}, nil
}

func isSharedHost(host string) bool {
	return host == "postgres" || host == "localhost" || host == "127.0.0.1" || host == ""
}

func (r *Resolver) getSharedPool(meta registry.RoutingMetadata) (*sql.DB, error) {
	r.sharedPoolMu.Lock()
	defer r.sharedPoolMu.Unlock()

	if r.sharedPool != nil {
		return r.sharedPool, nil
	}

	port := meta.DBPort
	if port <= 0 {
		port = 5432
	}
	user := meta.DBUser
	if user == "" {
		user = "postgres"
	}
	dbname := meta.DBName
	if dbname == "" {
		dbname = "postgres"
	}
	host := meta.DBHost
	if host == "" {
		host = "postgres"
	}

	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, r.sharedDBPass, dbname)

	client, err := postgres.NewClientFromDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open shared database pool: %w", err)
	}

	// Optimize multiplexing shared pool limits
	client.SetMaxOpenConns(25)
	client.SetMaxIdleConns(10)
	client.SetConnMaxLifetime(30 * time.Minute)
	client.SetConnMaxIdleTime(5 * time.Minute)

	r.sharedPool = client
	return r.sharedPool, nil
}

func (r *Resolver) openDedicatedPool(tenantID string, meta registry.RoutingMetadata) (*sql.DB, string, error) {
	pass := crypto.DeriveTenantDBPassword(r.sharedSecret, tenantID)

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
		return nil, "", fmt.Errorf("failed to open dedicated database pool for tenant '%s' (host '%s'): %w", tenantID, meta.DBHost, err)
	}

	return client, schema, nil
}

func (r *Resolver) fetchRoutingFromService(ctx context.Context, tenantID string) (registry.RoutingMetadata, error) {
	// Attempt to acquire a concurrency token without blocking.
	// If 50 fetches are already in-flight and the control plane is slow, the 51st
	// request returns immediately so the Gin HTTP worker goroutine is freed.
	// The client receives a retriable error; warm-tenant requests are unaffected.
	select {
	case r.httpSemaphore <- struct{}{}:
		defer func() { <-r.httpSemaphore }()
	case <-ctx.Done():
		return registry.RoutingMetadata{}, ctx.Err()
	default:
		// Fail fast — do not hoard a Gin HTTP worker goroutine.
		return registry.RoutingMetadata{}, fmt.Errorf(
			"tenantdb: control plane fetch limit reached, shedding load for tenant '%s'", tenantID,
		)
	}

	url := fmt.Sprintf("%s/internal/tenants/%s/infrastructure/order-service", r.tenantServiceURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return registry.RoutingMetadata{}, fmt.Errorf("failed to build routing request: %w", err)
	}

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

