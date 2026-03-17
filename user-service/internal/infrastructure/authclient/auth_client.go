package authclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type AuthClient struct {
	authServiceURL string
	httpClient     *http.Client
}

func NewAuthClient(authServiceURL string) *AuthClient {
	if authServiceURL == "" {
		authServiceURL = "http://auth-service:8085"
	}
	return &AuthClient{
		authServiceURL: authServiceURL,
		httpClient:     &http.Client{Timeout: 5 * time.Second},
	}
}

type Role struct {
	ID          string   `json:"id"`
	TenantID    *string  `json:"tenant_id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	IsSystem    bool     `json:"is_system"`
	Permissions []string `json:"permissions,omitempty"`
}

type PermissionCatalogItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Service     string `json:"service"`
	Description string `json:"description"`
}

type CreateRoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

type UserRoleResponse struct {
	UserID     string    `json:"user_id"`
	TenantID   string    `json:"tenant_id"`
	RoleID     string    `json:"role_id"`
	RoleName   string    `json:"role_name"`
	AssignedAt time.Time `json:"assigned_at"`
}

func (authClient *AuthClient) AssignUserRole(ctx context.Context, authToken, userID, roleID string) (*UserRoleResponse, error) {
	url := fmt.Sprintf("%s/api/auth/users/%s/role", authClient.authServiceURL, userID)
	bodyBytes, err := json.Marshal(map[string]string{"role_id": roleID})
	if err != nil {
		return nil, fmt.Errorf("auth client: failed to marshal assign role payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("auth client: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}

	resp, err := authClient.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth client: auth service call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth client: auth service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		Data UserRoleResponse `json:"data"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("auth client: failed to decode user role response: %w", err)
	}
	return &res.Data, nil
}

func (authClient *AuthClient) GetUserRole(ctx context.Context, authToken, userID string) (*UserRoleResponse, error) {
	url := fmt.Sprintf("%s/api/auth/users/%s/role", authClient.authServiceURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("auth client: failed to create request: %w", err)
	}
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}

	resp, err := authClient.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth client: auth service call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth client: auth service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		Data UserRoleResponse `json:"data"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("auth client: failed to decode user role response: %w", err)
	}
	return &res.Data, nil
}

func (authClient *AuthClient) CreateRole(ctx context.Context, authToken string, input CreateRoleInput) (*Role, error) {
	url := fmt.Sprintf("%s/api/auth/roles", authClient.authServiceURL)
	bodyBytes, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("auth client: failed to marshal create role payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("auth client: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}

	resp, err := authClient.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth client: auth service call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth client: auth service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		Data Role `json:"data"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("auth client: failed to decode role response: %w", err)
	}
	return &res.Data, nil
}

func (authClient *AuthClient) ListRoles(ctx context.Context, authToken string) ([]Role, error) {
	url := fmt.Sprintf("%s/api/auth/roles", authClient.authServiceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("auth client: failed to create request: %w", err)
	}
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}

	resp, err := authClient.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth client: auth service call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth client: auth service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		Data []Role `json:"data"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("auth client: failed to decode roles list response: %w", err)
	}
	return res.Data, nil
}

func (authClient *AuthClient) ListPermissions(ctx context.Context, authToken string) ([]PermissionCatalogItem, error) {
	url := fmt.Sprintf("%s/api/auth/permissions", authClient.authServiceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("auth client: failed to create request: %w", err)
	}
	if authToken != "" {
		req.Header.Set("Authorization", authToken)
	}

	resp, err := authClient.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth client: auth service call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth client: auth service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var res struct {
		Data []PermissionCatalogItem `json:"data"`
	}
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, fmt.Errorf("auth client: failed to decode permissions catalog response: %w", err)
	}
	return res.Data, nil
}
