package docker

import (
	"testing"
)

func TestValidateIdentifier(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "valid alphanumeric string",
			input:   "order_db",
			wantErr: false,
		},
		{
			name:    "valid string with numbers and underscores",
			input:   "tenant_123_user",
			wantErr: false,
		},
		{
			name:    "invalid containing hyphen",
			input:   "tenant-123",
			wantErr: true,
		},
		{
			name:    "invalid containing spaces",
			input:   "order db",
			wantErr: true,
		},
		{
			name:    "invalid containing SQL injection characters",
			input:   "user; DROP TABLE users;--",
			wantErr: true,
		},
		{
			name:    "invalid empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid special characters",
			input:   "db$name!",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIdentifier(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIdentifier(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestSanitizeTenantID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "converts uppercase to lowercase",
			input:    "TENANT123",
			expected: "tenant123",
		},
		{
			name:     "replaces hyphens with underscores",
			input:    "tenant-uuid-1234",
			expected: "tenant_uuid_1234",
		},
		{
			name:     "handles mixed case and hyphens",
			input:    "Tenant-UUID-ABC",
			expected: "tenant_uuid_abc",
		},
		{
			name:     "already sanitized string remains unchanged",
			input:    "tenant_123_abc",
			expected: "tenant_123_abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeTenantID(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeTenantID(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNewDockerProvisioner_DefaultNetwork(t *testing.T) {
	// NewDockerProvisioner creates a Docker client via client.NewClientWithOpts.
	// When running unit tests, client creation succeeds even if Docker daemon is not running.
	prov, err := NewDockerProvisioner("")
	if err != nil {
		t.Fatalf("unexpected error creating DockerProvisioner: %v", err)
	}

	if prov == nil {
		t.Fatal("expected non-nil DockerProvisioner")
	}

	if prov.networkName != "microservice-network" {
		t.Errorf("expected default network 'microservice-network', got %q", prov.networkName)
	}
}

func TestNewDockerProvisioner_CustomNetwork(t *testing.T) {
	prov, err := NewDockerProvisioner("custom-network")
	if err != nil {
		t.Fatalf("unexpected error creating DockerProvisioner: %v", err)
	}

	if prov == nil {
		t.Fatal("expected non-nil DockerProvisioner")
	}

	if prov.networkName != "custom-network" {
		t.Errorf("expected network 'custom-network', got %q", prov.networkName)
	}
}
