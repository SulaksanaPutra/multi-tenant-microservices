package middleware

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"tenant-service/internal/repository"
	"tenant-service/internal/types"

	_ "github.com/lib/pq"
)

type HandlerFactory func(cfg types.TenantConfig) http.HandlerFunc

type TenantMiddleware struct {
	controlPlaneRepo repository.ControlPlaneRepository
	sharedDB         *sql.DB
	mu               sync.RWMutex
	pools            map[string]*sql.DB
}

func NewTenantMiddleware(controlPlaneRepo repository.ControlPlaneRepository, sharedDB *sql.DB) *TenantMiddleware {
	return &TenantMiddleware{
		controlPlaneRepo: controlPlaneRepo,
		sharedDB:         sharedDB,
		pools:            make(map[string]*sql.DB),
	}
}

// GetConnection returns the sql.DB handle for a given tenant.
// For SHARED placement, it returns the shared database pool.
// For DEDICATED placement, it lazily creates or retrieves a cached pool for the tenant's DSN.
func (m *TenantMiddleware) GetConnection(meta *repository.TenantMetadata) (*sql.DB, error) {
	if meta == nil || meta.PlacementType == "SHARED" || meta.PlacementType == "" {
		return m.sharedDB, nil
	}

	// Read lock check
	m.mu.RLock()
	pool, exists := m.pools[meta.ID]
	m.mu.RUnlock()

	if exists {
		return pool, nil
	}

	// Write lock creation
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double check
	if pool, exists := m.pools[meta.ID]; exists {
		return pool, nil
	}

	if meta.DbDSN == "" {
		return nil, fmt.Errorf("dedicated tenant %s has no DbDSN configured", meta.ID)
	}

	newPool, err := sql.Open("postgres", meta.DbDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database pool for dedicated tenant %s: %w", meta.ID, err)
	}

	newPool.SetMaxOpenConns(15)
	newPool.SetMaxIdleConns(5)
	newPool.SetConnMaxLifetime(30 * time.Minute)

	if err := newPool.Ping(); err != nil {
		newPool.Close()
		return nil, fmt.Errorf("failed to ping database for dedicated tenant %s: %w", meta.ID, err)
	}

	log.Printf("TenantMiddleware: Created persistent pool for dedicated tenant '%s' (%s)", meta.ID, meta.Name)
	m.pools[meta.ID] = newPool
	return newPool, nil
}

// GetAllActiveDedicatedPools returns all registered dedicated database pools.
func (m *TenantMiddleware) GetAllActiveDedicatedPools() map[string]*sql.DB {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copied := make(map[string]*sql.DB, len(m.pools))
	for k, v := range m.pools {
		copied[k] = v
	}
	return copied
}

func (m *TenantMiddleware) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for tenantID, pool := range m.pools {
		log.Printf("TenantMiddleware: Closing pool for dedicated tenant '%s'", tenantID)
		pool.Close()
	}
	m.pools = make(map[string]*sql.DB)
}

// ResolveTenant intercepts the HTTP request, resolves tenant metadata & DB pool,
// constructs a types.TenantConfig struct, and invokes the handler factory.
func (m *TenantMiddleware) ResolveTenant(factory HandlerFactory) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Tenant-ID")
		if tenantID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing X-Tenant-ID header"})
			return
		}

		meta, err := m.controlPlaneRepo.GetTenantMetadataBySlug(r.Context(), tenantID)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "tenant not found"})
			return
		}

		pool, err := m.GetConnection(meta)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to resolve tenant database connection"})
			return
		}

		targetSchema := meta.SchemaName
		if meta.PlacementType == "DEDICATED" {
			targetSchema = "public"
		}

		cfg := types.TenantConfig{
			TenantID:      meta.ID,
			PlacementType: meta.PlacementType,
			DB:            pool,
			TargetSchema:  targetSchema,
		}

		handlerFunc := factory(cfg)
		handlerFunc(w, r)
	}
}
