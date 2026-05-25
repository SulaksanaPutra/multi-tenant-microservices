package mailer

import (
	"strings"
	"testing"
)

func TestNewMailer(t *testing.T) {
	m1 := NewMailer("localhost", "1025", "")
	if m1.from != "no-reply@company.com" {
		t.Errorf("expected default fallback from email 'no-reply@company.com', got '%s'", m1.from)
	}

	m2 := NewMailer("localhost", "1025", "custom@company.com")
	if m2.from != "custom@company.com" {
		t.Errorf("expected custom from email 'custom@company.com', got '%s'", m2.from)
	}
}

func TestBuildWelcomeMessage_IncludesTenantInfo(t *testing.T) {
	subject, body := buildWelcomeMessage(
		"tenant-acme",
		"Acme Corp",
		"acme-corp",
		"Bob Jones",
		"tok_123",
	)

	if subject != "Welcome to Acme Corp!" {
		t.Errorf("expected subject 'Welcome to Acme Corp!', got '%s'", subject)
	}
	if !strings.Contains(body, "Hello Bob Jones,") {
		t.Errorf("expected greeting 'Hello Bob Jones,', got:\n%s", body)
	}
	if !strings.Contains(body, `workspace "Acme Corp"`) {
		t.Errorf("expected body to reference workspace \"Acme Corp\", got:\n%s", body)
	}
	if !strings.Contains(body, "Workspace slug: acme-corp") {
		t.Errorf("expected body to include workspace slug, got:\n%s", body)
	}
	if !strings.Contains(body, "http://localhost:8000/setup-password?token=tok_123") {
		t.Errorf("expected body to include the setup link, got:\n%s", body)
	}
}

func TestBuildWelcomeMessage_FallsBackToTenantID(t *testing.T) {
	subject, body := buildWelcomeMessage(
		"tenant-legacy",
		"", // legacy events do not carry a tenant name
		"",
		"",
		"tok_456",
	)

	if subject != "Welcome! Your Tenant Workspace is Ready" {
		t.Errorf("expected generic fallback subject, got '%s'", subject)
	}
	if !strings.Contains(body, "Hello,") {
		t.Errorf("expected generic greeting fallback, got:\n%s", body)
	}
	if !strings.Contains(body, `workspace "tenant-legacy"`) {
		t.Errorf("expected body to fall back to tenant ID, got:\n%s", body)
	}
}
