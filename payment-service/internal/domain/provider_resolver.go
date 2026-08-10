package domain

import "context"

type ProviderCredentials struct {
	APIKey        string            `json:"api_key"`
	SecretKey     string            `json:"secret_key"`
	WebhookSecret string            `json:"webhook_secret"`
	ExtraOptions  map[string]string `json:"extra_options,omitempty"`
}

type TenantPSPConfig struct {
	TenantID        string
	PriorityChain   []ProviderType
	ProviderConfigs map[ProviderType]ProviderCredentials
}

type TenantPSPResolver interface {
	ResolveConfig(ctx context.Context, tenantID string) (*TenantPSPConfig, error)
}
