package authclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type AuthClient struct {
	authServiceURL string
	internalToken  string
	httpClient     *http.Client
}

func NewAuthClient(authServiceURL, internalToken string) *AuthClient {
	if authServiceURL == "" {
		authServiceURL = "http://auth-service:8085"
	}
	if internalToken == "" {
		internalToken = "default_internal_service_token"
	}
	return &AuthClient{
		authServiceURL: authServiceURL,
		internalToken:  internalToken,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

type createSetupTokenRequest struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
}

type createSetupTokenResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Data    struct {
		Token string `json:"token"`
	} `json:"data"`
}

func (c *AuthClient) FetchSetupToken(ctx context.Context, userID, tenantID, email string) (string, error) {
	reqBody, err := json.Marshal(createSetupTokenRequest{
		UserID:   userID,
		TenantID: tenantID,
		Email:    email,
	})
	if err != nil {
		return "", fmt.Errorf("auth client: failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/internal/auth/setup-token", c.authServiceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("auth client: failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Token", c.internalToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("auth client: failed to execute request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth client: unexpected status code %d from %s", resp.StatusCode, url)
	}

	var res createSetupTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", fmt.Errorf("auth client: failed to decode response: %w", err)
	}

	if res.Data.Token == "" {
		return "", fmt.Errorf("auth client: empty setup token returned from auth-service")
	}

	return res.Data.Token, nil
}
