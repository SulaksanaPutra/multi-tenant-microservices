package tenantclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"auth-service/internal/service"
)

const defaultTenantServiceURL = "http://tenant-service:8082"

type profileResponse struct {
	Data struct {
		TenantID string `json:"tenant_id"`
		Name     string `json:"name"`
		Slug     string `json:"slug"`
		Plan     string `json:"plan"`
		Status   string `json:"status"`
	} `json:"data"`
}

// TenantProfileClient is a Layer-3 outbound adapter that resolves tenant
// control-plane metadata from tenant-service for workspace enrichment.
type TenantProfileClient struct {
	tenantServiceURL     string
	internalServiceToken string
	httpClient           *http.Client
}

type Params struct {
	TenantServiceURL     string
	InternalServiceToken string
}

func NewTenantProfileClient(params Params) *TenantProfileClient {
	url := params.TenantServiceURL
	if url == "" {
		url = defaultTenantServiceURL
	}
	return &TenantProfileClient{
		tenantServiceURL:     url,
		internalServiceToken: params.InternalServiceToken,
		httpClient: &http.Client{
			Timeout: 3 * time.Second,
		},
	}
}

// GetTenantProfile resolves control-plane metadata for a workspace. Errors are
// returned so callers (AuthService) can treat enrichment as best-effort.
func (c *TenantProfileClient) GetTenantProfile(ctx context.Context, tenantID string) (*service.TenantProfile, error) {
	url := fmt.Sprintf("%s/internal/tenants/%s/profile", c.tenantServiceURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("tenant profile client: failed to build request: %w", err)
	}

	req.Header.Set("X-Internal-Service-Token", c.internalServiceToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tenant profile client: failed to fetch profile for tenant '%s': %w", tenantID, err)
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tenant profile client: tenant-service returned status %d for tenant '%s'", resp.StatusCode, tenantID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tenant profile client: failed to read response body: %w", err)
	}

	var res profileResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("tenant profile client: failed to parse response JSON: %w", err)
	}

	return &service.TenantProfile{
		TenantID: res.Data.TenantID,
		Name:     res.Data.Name,
		Slug:     res.Data.Slug,
		Plan:     res.Data.Plan,
	}, nil
}