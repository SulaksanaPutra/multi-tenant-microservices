package domain

type ProviderCredentials struct {
	APIKey        string            `json:"api_key"`
	SecretKey     string            `json:"secret_key"`
	WebhookSecret string            `json:"webhook_secret"`
	ExtraOptions  map[string]string `json:"extra_options,omitempty"`
}

type PaymentMethodConfig struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Type          InstructionType `json:"type"`
	Enabled       bool            `json:"enabled"`
	PriorityChain []ProviderType  `json:"priority_chain"`
}

type TenantPSPConfig struct {
	TenantID        string
	Methods         []PaymentMethodConfig
	ProviderConfigs map[ProviderType]ProviderCredentials
}

