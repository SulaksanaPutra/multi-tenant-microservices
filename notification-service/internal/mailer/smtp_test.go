package mailer

import (
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
