package mailer

import (
	"fmt"
	"log"
	"net/smtp"
	"strings"
)

type Mailer struct {
	smtpHost string
	smtpPort string
	from     string
}

func NewMailer(host, port, from string) *Mailer {
	if from == "" {
		from = "no-reply@company.com"
	}
	return &Mailer{
		smtpHost: host,
		smtpPort: port,
		from:     from,
	}
}

func (m *Mailer) SendWelcomeEmail(toEmail, tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string, error) {
	subject, body := buildWelcomeMessage(tenantID, tenantName, tenantSlug, ownerName, setupToken)

	addr := fmt.Sprintf("%s:%s", m.smtpHost, m.smtpPort)

	msg := []string{
		fmt.Sprintf("From: %s", m.from),
		fmt.Sprintf("To: %s", toEmail),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}
	messageBytes := []byte(strings.Join(msg, "\r\n"))

	err := smtp.SendMail(addr, nil, m.from, []string{toEmail}, messageBytes)
	if err != nil {
		return subject, body, fmt.Errorf("failed to send SMTP email via %s: %w", addr, err)
	}

	log.Printf("Successfully sent welcome email to %s via Mailpit SMTP (%s)", toEmail, addr)
	return subject, body, nil
}

// buildWelcomeMessage renders the welcome email subject and body. The display
// name is always human-readable (either the tenant name or a fallback to the
// tenant ID), and the greeting is personalized with the owner's name when
// available. Kept as a pure function so the copy is unit-testable without
// touching SMTP.
func buildWelcomeMessage(tenantID, tenantName, tenantSlug, ownerName, setupToken string) (string, string) {
	setupLink := fmt.Sprintf("http://localhost:8000/setup-password?token=%s", setupToken)

	displayName := tenantName
	if strings.TrimSpace(displayName) == "" {
		displayName = tenantID
	}

	subject := "Welcome! Your Tenant Workspace is Ready"
	if strings.TrimSpace(tenantName) != "" {
		subject = fmt.Sprintf("Welcome to %s!", tenantName)
	}

	greeting := "Hello"
	if strings.TrimSpace(ownerName) != "" {
		greeting = fmt.Sprintf("Hello %s", ownerName)
	}

	body := fmt.Sprintf(
		"%s,\n\nGreat news — your workspace \"%s\" has been successfully provisioned and is ready for use.",
		greeting,
		displayName,
	)
	if strings.TrimSpace(tenantSlug) != "" {
		body += fmt.Sprintf("\nWorkspace slug: %s", tenantSlug)
	}
	body += fmt.Sprintf("\n\nPlease set up your password to activate your account:\n%s\n\nThank you for choosing our platform!", setupLink)

	return subject, body
}
