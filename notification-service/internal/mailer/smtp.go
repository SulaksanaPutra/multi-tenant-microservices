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

func (m *Mailer) SendWelcomeEmail(toEmail, tenantID string) (string, string, error) {
	subject := "Welcome! Your Tenant Workspace is Ready"
	body := fmt.Sprintf("Hello,\n\nYour tenant workspace '%s' has been successfully provisioned and is ready for use.\n\nThank you for choosing our platform!", tenantID)

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
