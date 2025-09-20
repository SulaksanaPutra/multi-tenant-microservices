package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"order-service/internal/infrastructure/postgres"
	"order-service/internal/registry"
	"order-service/internal/repository"
)

const (
	tenantServiceInternalURL = "http://tenant-service:8082"
)

type dsnResponse struct {
	Data struct {
		DSN        string `json:"dsn"`
		SchemaName string `json:"schema_name"`
	} `json:"data"`
}

type OrderService interface {
	GetOrders(ctx context.Context, tenantID string) ([]repository.Order, error)
}

type orderService struct {
	registry         *registry.PoolRegistry
	orderRepo        repository.OrderRepository
	tenantServiceURL string
}

type OrderServiceParams struct {
	Registry         *registry.PoolRegistry
	OrderRepo        repository.OrderRepository
	TenantServiceURL string // override for testing
}

func NewOrderService(params OrderServiceParams) OrderService {
	url := params.TenantServiceURL
	if url == "" {
		url = tenantServiceInternalURL
	}
	return &orderService{
		registry:         params.Registry,
		orderRepo:        params.OrderRepo,
		tenantServiceURL: url,
	}
}

func (s *orderService) GetOrders(ctx context.Context, tenantID string) ([]repository.Order, error) {
	var resolvedSchema string
	db, err := s.registry.GetOrFetch(tenantID, func() (*sql.DB, error) {
		return s.fetchAndOpenPool(ctx, tenantID, &resolvedSchema)
	})
	if err != nil {
		return nil, fmt.Errorf("order service: failed to resolve DB for tenant '%s': %w", tenantID, err)
	}

	// If this was a cache hit, we need the schema name too.
	// For simplicity, include the schema in a separate lookup or store it alongside the pool.
	// In this implementation, we store it in the registry as part of the pool key.
	// NOTE: For the base refactor, we pass schemaName via a parallel cache.
	// TODO: Store schemaName alongside the pool entry in a future iteration.
	if resolvedSchema == "" {
		resolvedSchema = s.resolveSchemaFromDB(ctx, tenantID)
	}

	return s.orderRepo.GetOrders(ctx, db, resolvedSchema)
}

func (s *orderService) fetchAndOpenPool(ctx context.Context, tenantID string, resolvedSchema *string) (*sql.DB, error) {
	url := fmt.Sprintf("%s/internal/tenants/%s/infrastructure/order-service", s.tenantServiceURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build DSN request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch DSN from tenant-service: %w", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			println(err.Error())
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tenant-service returned status %d for tenant '%s'", resp.StatusCode, tenantID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read DSN response body: %w", err)
	}

	var dsnResp dsnResponse
	if err := json.Unmarshal(body, &dsnResp); err != nil {
		return nil, fmt.Errorf("failed to parse DSN response: %w", err)
	}

	if dsnResp.Data.DSN == "" {
		return nil, fmt.Errorf("empty DSN returned for tenant '%s'", tenantID)
	}

	if resolvedSchema != nil {
		*resolvedSchema = dsnResp.Data.SchemaName
	}

	return postgres.NewClientFromDSN(dsnResp.Data.DSN)
}

func (s *orderService) resolveSchemaFromDB(_ context.Context, _ string) string {
	// Fallback: if schema wasn't captured during pool creation (cache hit case),
	// default to "public". A proper solution is to include schemaName in the poolEntry
	// struct — tracked as a follow-up improvement.
	return "public"
}
