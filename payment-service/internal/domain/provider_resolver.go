package domain

type ProviderCredentials struct {
	APIKey        string
	SecretKey     string
	WebhookSecret string
	ExtraOptions  map[string]string
}

type PaymentMethodConfig struct {
	ID            string
	Name          string
	Type          InstructionType
	Enabled       bool
	PriorityChain []ProviderType
}

type TenantPSPConfig struct {
	TenantID        string
	Methods         []PaymentMethodConfig
	ProviderConfigs map[ProviderType]ProviderCredentials
}
