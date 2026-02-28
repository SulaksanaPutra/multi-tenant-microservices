package authclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type PermissionItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type registerPayload struct {
	Service     string           `json:"service"`
	Permissions []PermissionItem `json:"permissions"`
}

type PermissionRegistrar struct {
	authServiceURL       string
	internalServiceToken string
	httpSemaphore        chan struct{}
	registered           atomic.Bool
}

func NewPermissionRegistrar(authServiceURL, internalServiceToken string) *PermissionRegistrar {
	if authServiceURL == "" {
		authServiceURL = "http://auth-service:8085"
	}
	if internalServiceToken == "" {
		internalServiceToken = "default_internal_service_token"
	}
	return &PermissionRegistrar{
		authServiceURL:       authServiceURL,
		internalServiceToken: internalServiceToken,
		httpSemaphore:        make(chan struct{}, 50),
	}
}

func (r *PermissionRegistrar) Register(ctx context.Context, serviceName string, permissions []PermissionItem) error {
	if r.registered.Load() {
		return nil
	}

	var lastErr error
	for attempt := 1; attempt <= 10; attempt++ {
		err := r.doRegister(ctx, serviceName, permissions)
		if err == nil {
			r.registered.Store(true)
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return lastErr
}

func (r *PermissionRegistrar) doRegister(ctx context.Context, serviceName string, permissions []PermissionItem) error {
	select {
	case r.httpSemaphore <- struct{}{}:
		defer func() { <-r.httpSemaphore }()
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("permission registrar: semaphore limit reached, deferring registration for '%s'", serviceName)
	}

	bodyBytes, err := json.Marshal(registerPayload{
		Service:     serviceName,
		Permissions: permissions,
	})
	if err != nil {
		return fmt.Errorf("permission registrar: failed to marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/internal/permissions/register", r.authServiceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("permission registrar: failed to build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Token", r.internalServiceToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("permission registrar: failed to POST permissions to auth-service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("permission registrar: auth-service returned status %d", resp.StatusCode)
	}

	return nil
}
