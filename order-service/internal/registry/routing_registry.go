package registry

import (
	"log"
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
	// Status reflects the tenant's current migration state.
	// Valid values: "" (normal), "MIGRATING" (distributed lock active).
	Status string `json:"status,omitempty"`
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

// GetStatus returns the current Status string for a tenant ("" if not found). Thread-safe.
func (r *RoutingRegistry) GetStatus(tenantID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.routes[tenantID].Status
}

// TenantIDs returns a snapshot of all tenant IDs currently materialized in the registry.
// Used by the outbox worker to enumerate the physical outbox tables that need polling.
// Thread-safe.
func (r *RoutingRegistry) TenantIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.routes))
	for id := range r.routes {
		ids = append(ids, id)
	}
	return ids
}

// SetStatus updates only the Status field for an existing tenant entry.
// If no entry exists, it creates a minimal one with just the status set.
// Thread-safe.
func (r *RoutingRegistry) SetStatus(tenantID, status string) {
	if tenantID == "" {
		return
	}
	r.mu.Lock()
	meta := r.routes[tenantID]
	meta.TenantID = tenantID
	meta.Status = status
	r.routes[tenantID] = meta
	r.mu.Unlock() // Release before logging — no need to hold lock during I/O.
	log.Printf("RoutingRegistry: Status for tenant='%s' set to '%s'", tenantID, status)
}

// Delete removes routing metadata for a tenant. Thread-safe.
func (r *RoutingRegistry) Delete(tenantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.routes, tenantID)
}

// PurgeAll clears all stored tenant routing metadata using an O(1) map swap. Thread-safe.
func (r *RoutingRegistry) PurgeAll() {
	r.mu.Lock()
	count := len(r.routes)
	r.routes = make(map[string]RoutingMetadata)
	r.mu.Unlock() // Release before logging — no need to hold lock during I/O.
	log.Printf("RoutingRegistry: Purged all tenant routes (%d routes evicted)", count)
}

