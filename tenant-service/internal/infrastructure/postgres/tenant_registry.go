package postgres

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

type TenantMetadata struct {
	ID            string
	Name          string
	PlacementType string // "SHARED" or "DEDICATED"
	SchemaName    string // e.g. "tenant_123" (if SHARED)
	DbDSN         string // Connection string (if DEDICATED)
}

type ConnectionRegistry struct {
	mu       sync.RWMutex
	pools    map[string]*sql.DB
	sharedDB *sql.DB
}

func NewConnectionRegistry(sharedDB *sql.DB) *ConnectionRegistry {
	return &ConnectionRegistry{
		pools:    make(map[string]*sql.DB),
		sharedDB: sharedDB,
	}
}

// GetConnection returns the sql.DB handle for a given tenant.
// For SHARED placement, it returns the shared database pool.
// For DEDICATED placement, it lazily creates or retrieves a cached pool for the tenant's DSN.
func (r *ConnectionRegistry) GetConnection(tenant *TenantMetadata) (*sql.DB, error) {
	if tenant == nil || tenant.PlacementType == "SHARED" || tenant.PlacementType == "" {
		return r.sharedDB, nil
	}

	// Read lock check
	r.mu.RLock()
	pool, exists := r.pools[tenant.ID]
	r.mu.RUnlock()

	if exists {
		return pool, nil
	}

	// Write lock creation
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double check
	if pool, exists := r.pools[tenant.ID]; exists {
		return pool, nil
	}

	if tenant.DbDSN == "" {
		return nil, fmt.Errorf("dedicated tenant %s has no DbDSN configured", tenant.ID)
	}

	newPool, err := sql.Open("postgres", tenant.DbDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database pool for dedicated tenant %s: %w", tenant.ID, err)
	}

	// Set pool limits for dedicated pool to avoid resource exhaustion
	newPool.SetMaxOpenConns(15)
	newPool.SetMaxIdleConns(5)
	newPool.SetConnMaxLifetime(30 * time.Minute)

	if err := newPool.Ping(); err != nil {
		newPool.Close()
		return nil, fmt.Errorf("failed to ping database for dedicated tenant %s: %w", tenant.ID, err)
	}

	log.Printf("ConnectionRegistry: Created persistent pool for dedicated tenant '%s' (%s)", tenant.ID, tenant.Name)
	r.pools[tenant.ID] = newPool
	return newPool, nil
}

// GetAllActiveDedicatedPools returns all registered dedicated database pools.
func (r *ConnectionRegistry) GetAllActiveDedicatedPools() map[string]*sql.DB {
	r.mu.RLock()
	defer r.mu.RUnlock()

	copied := make(map[string]*sql.DB, len(r.pools))
	for k, v := range r.pools {
		copied[k] = v
	}
	return copied
}

func (r *ConnectionRegistry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for tenantID, pool := range r.pools {
		log.Printf("ConnectionRegistry: Closing pool for dedicated tenant '%s'", tenantID)
		pool.Close()
	}
	r.pools = make(map[string]*sql.DB)
}
