package registry

import (
	"sync"
)

// RoutingMetadata represents non-sensitive infrastructure routing information for a tenant.
type RoutingMetadata struct {
	TenantID   string `json:"tenant_id"`
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	SchemaName string `json:"schema_name"`
}

// RoutingRegistry is a thread-safe in-memory materialized view of tenant routing metadata.
// It allows order-service to resolve DSN metadata with 0ms local RAM lookup instead of synchronous HTTP calls.
type RoutingRegistry struct {
	mu     sync.RWMutex
	routes map[string]RoutingMetadata
}

func NewRoutingRegistry() *RoutingRegistry {
	return &RoutingRegistry{
		routes: make(map[string]RoutingMetadata),
	}
}

// Set stores or updates routing metadata for a tenant. Thread-safe.
func (r *RoutingRegistry) Set(meta RoutingMetadata) {
	if meta.TenantID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[meta.TenantID] = meta
}

// Get retrieves routing metadata for a tenant from local memory. Thread-safe.
func (r *RoutingRegistry) Get(tenantID string) (RoutingMetadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	meta, ok := r.routes[tenantID]
	return meta, ok
}

// Delete removes routing metadata for a tenant. Thread-safe.
func (r *RoutingRegistry) Delete(tenantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.routes, tenantID)
}

// PurgeAll clears all stored tenant routing metadata. Thread-safe.
func (r *RoutingRegistry) PurgeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := len(r.routes)
	r.routes = make(map[string]RoutingMetadata)
	log.Printf("RoutingRegistry: Purged all tenant routes (%d routes evicted)", count)
}
