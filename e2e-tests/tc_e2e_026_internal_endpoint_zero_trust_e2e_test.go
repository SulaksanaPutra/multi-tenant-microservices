/*
 * Test Specification: TC-E2E-026 - Internal Zero-Trust Endpoint Boundary (Docs Cases #8 & #9)
 * Architectural Scope: auth-service (/internal/auth/*), tenant-service (/internal/tenants/*),
 *                      InternalAuthMiddleware (X-Internal-Service-Token), Gateway.
 * Objective: Validate the zero-trust control-plane boundary: internal endpoints must reject requests
 *            without a valid X-Internal-Service-Token, and must never leak infrastructure metadata
 *            through path traversal, unknown service names, or nonexistent tenants.
 * Failure Mode Guarded: Lateral movement into control-plane internals, SSRF/host-takeover via service_name,
 *                       cross-service credential theft, infrastructure metadata leakage.
 *
 * Workflow / How It Works:
 *   1. Register a shared tenant and resolve a real tenant_id / user_id for positive-control probes.
 *   2. auth-service internal endpoints:
 *        - POST /internal/auth/setup-token without token           -> HTTP 403 Forbidden
 *        - POST /internal/auth/setup-token with forged token       -> HTTP 403 Forbidden
 *        - POST /internal/auth/setup-token with valid token        -> HTTP 200 OK (positive control)
 *        - GET  /internal/auth/users/:userID/perm-version without token -> HTTP 403 Forbidden
 *        - GET  /internal/auth/users/:userID/perm-version with valid token -> HTTP 200 OK
 *   3. tenant-service internal infrastructure endpoint:
 *        - GET /internal/tenants/:id/infrastructure/order-service without token -> HTTP 403
 *        - GET with forged token            -> HTTP 403
 *        - GET with valid token             -> HTTP 200 (positive control)
 *        - GET with traversal service_name  -> HTTP 404 (no metadata leak)
 *        - GET with nonexistent tenant      -> HTTP 404
 */

package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

const tenantServiceInternalURL = "http://localhost:8082"

func TestE2E_TC_E2E_026_InternalEndpointZeroTrustBoundary(t *testing.T) {
	t.Log("=== TC-E2E-026: Internal Zero-Trust Endpoint Boundary (Docs Cases #8 & #9) ===")

	// Resolve the inter-service bearer token (env override supported, compose default fallback).
	validInternalToken := internalServiceToken()

	// =========================================================================
	// Step 1: Provision Tenant & Resolve User ID (for positive-control probes)
	// =========================================================================
	tenantID, userID, ownerEmail, _ := registerAndActivateTenant(t)
	t.Logf("2. Resolved tenant_id='%s' user_id='%s' for boundary probes.", tenantID, userID)

	// =========================================================================
	// Step 2: auth-service /internal/auth/setup-token — token enforcement
	// Instruction: Probe the internal setup-token endpoint with missing, forged, and valid tokens.
	// Architectural Invariant: InternalAuthMiddleware returns 403 for missing/invalid X-Internal-Service-Token.
	// =========================================================================
	setupTokenBody, _ := json.Marshal(map[string]string{
		"user_id":   userID,
		"tenant_id": tenantID,
		"email":     ownerEmail,
	})

	// 2a. Missing token -> 403
	reqMissing, _ := http.NewRequest(http.MethodPost, authServiceURL+"/internal/auth/setup-token", bytes.NewBuffer(setupTokenBody))
	reqMissing.Header.Set("Content-Type", "application/json")
	assertStatus(t, reqMissing, http.StatusForbidden, "setup-token without internal token")

	// 2b. Forged token -> 403
	reqForged, _ := http.NewRequest(http.MethodPost, authServiceURL+"/internal/auth/setup-token", bytes.NewBuffer(setupTokenBody))
	reqForged.Header.Set("Content-Type", "application/json")
	reqForged.Header.Set("X-Internal-Service-Token", "forged_internal_secret")
	assertStatus(t, reqForged, http.StatusForbidden, "setup-token with forged internal token")

	// 2c. Valid token -> 200 (positive control proving the endpoint is token-gated, not hidden)
	reqValid, _ := http.NewRequest(http.MethodPost, authServiceURL+"/internal/auth/setup-token", bytes.NewBuffer(setupTokenBody))
	reqValid.Header.Set("Content-Type", "application/json")
	reqValid.Header.Set("X-Internal-Service-Token", validInternalToken)
	assertStatus(t, reqValid, http.StatusOK, "setup-token with valid internal token")

	// =========================================================================
	// Step 3: auth-service /internal/auth/users/:userID/perm-version — token enforcement
	// =========================================================================
	permVersionURL := authServiceURL + "/internal/auth/users/" + userID + "/perm-version?tenant_id=" + tenantID

	reqPVMissing, _ := http.NewRequest(http.MethodGet, permVersionURL, nil)
	assertStatus(t, reqPVMissing, http.StatusForbidden, "perm-version without internal token")

	reqPVValid, _ := http.NewRequest(http.MethodGet, permVersionURL, nil)
	reqPVValid.Header.Set("X-Internal-Service-Token", validInternalToken)
	assertStatus(t, reqPVValid, http.StatusOK, "perm-version with valid internal token")

	// =========================================================================
	// Step 4: tenant-service /internal/tenants/:id/infrastructure/:service — token enforcement
	// Architectural Invariant: Zero-Trust routing metadata is only served to authenticated services.
	// =========================================================================
	infraURL := tenantServiceInternalURL + "/internal/tenants/" + tenantID + "/infrastructure/order-service"

	reqInfraMissing, _ := http.NewRequest(http.MethodGet, infraURL, nil)
	assertStatus(t, reqInfraMissing, http.StatusForbidden, "infrastructure lookup without internal token")

	reqInfraForged, _ := http.NewRequest(http.MethodGet, infraURL, nil)
	reqInfraForged.Header.Set("X-Internal-Service-Token", "forged_internal_secret")
	assertStatus(t, reqInfraForged, http.StatusForbidden, "infrastructure lookup with forged internal token")

	reqInfraValid, _ := http.NewRequest(http.MethodGet, infraURL, nil)
	reqInfraValid.Header.Set("X-Internal-Service-Token", validInternalToken)
	assertStatus(t, reqInfraValid, http.StatusOK, "infrastructure lookup with valid internal token")

	// =========================================================================
	// Step 5: tenant-service infrastructure lookup — traversal / unknown-service rejection
	// Instruction: Attempt to smuggle a path traversal or an unknown service name with a VALID token.
	// Architectural Invariant: service_name is resolved strictly against tenant_infrastructures;
	//                          unknown names yield 404, never raw metadata or host access.
	// =========================================================================
	traversalURL := tenantServiceInternalURL + "/internal/tenants/" + tenantID + "/infrastructure/..%2f..%2fetc%2fpasswd"
	reqTraversal, _ := http.NewRequest(http.MethodGet, traversalURL, nil)
	reqTraversal.Header.Set("X-Internal-Service-Token", validInternalToken)
	assertStatus(t, reqTraversal, http.StatusNotFound, "infrastructure lookup with path-traversal service_name")

	// Unknown service name (SSRF-ish probe) -> 404
	unknownServiceURL := tenantServiceInternalURL + "/internal/tenants/" + tenantID + "/infrastructure/inventory-service"
	reqUnknownSvc, _ := http.NewRequest(http.MethodGet, unknownServiceURL, nil)
	reqUnknownSvc.Header.Set("X-Internal-Service-Token", validInternalToken)
	assertStatus(t, reqUnknownSvc, http.StatusNotFound, "infrastructure lookup with unknown service_name")

	// Nonexistent tenant -> 404
	missingTenantURL := tenantServiceInternalURL + "/internal/tenants/tnt_does_not_exist/infrastructure/order-service"
	reqMissingTenant, _ := http.NewRequest(http.MethodGet, missingTenantURL, nil)
	reqMissingTenant.Header.Set("X-Internal-Service-Token", validInternalToken)
	assertStatus(t, reqMissingTenant, http.StatusNotFound, "infrastructure lookup with nonexistent tenant")

	t.Logf("6. TC-E2E-026 Passed: Internal zero-trust boundary strictly enforced (missing/forged tokens rejected, metadata never leaked).")
}

// assertStatus executes the request and fails the test unless the response status matches expected.
func assertStatus(t *testing.T, req *http.Request, expected int, label string) {
	t.Helper()

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("[%s] request failed: %v", label, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != expected {
		t.Fatalf("[%s] expected HTTP %d, got %d", label, expected, resp.StatusCode)
	}
	t.Logf("[%s] verified HTTP %d.", label, expected)
}
