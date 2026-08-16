package authsrv

import (
	"fmt"
	"log"
	"net/smtp"
	"os"
	"strings"
)

// Mailer delivers verification emails. The interface keeps the registration
// mechanism (pending + token + verify link) decoupled from delivery so a
// console mailer works out of the box and SMTP can be dropped in.
type Mailer interface {
	// SendVerification delivers the verification link for a registration.
	SendVerification(email, verifyURL string) error
}

// ConsoleMailer prints the verification link to the server log. The default
// for local / self-hosted deployments.
type ConsoleMailer struct{}

func (ConsoleMailer) SendVerification(email, verifyURL string) error {
	log.Printf("[mail] To: %s\n[mail] Subject: Verify your Astra account\n[mail] Verify: %s",
		email, verifyURL)
	return nil
}

// SMTPMailer sends verification email via a plain SMTP server.
// Configure with SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASS / SMTP_FROM.
type SMTPMailer struct {
	Host string
	Port string
	User string
	Pass string
	From string
}

func NewSMTPMailerFromEnv() *SMTPMailer {
	return &SMTPMailer{
		Host: getenv("SMTP_HOST"),
		Port: getenv("SMTP_PORT"),
		User: getenv("SMTP_USER"),
		Pass: getenv("SMTP_PASS"),
		From: getenv("SMTP_FROM"),
	}
}

func (m *SMTPMailer) Enabled() bool {
	return m.Host != ""
}

func (m *SMTPMailer) SendVerification(email, verifyURL string) error {
	from := m.From
	if from == "" {
		from = "no-reply@astra.dev"
	}
	subject := "Verify your Astra account"
	body := fmt.Sprintf("Verify your Astra account\n\nOpen this link to activate your account:\n%s\n\nIf you did not create an account, you can ignore this email.\n", verifyURL)
	msg := strings.Join([]string{
		"From: " + from,
		"To: " + email,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")
	addr := m.Host + ":" + m.Port
	auth := smtp.PlainAuth("", m.User, m.Pass, m.Host)
	return smtp.SendMail(addr, auth, from, []string{email}, []byte(msg))
}

func getenv(k string) string {
	return os.Getenv(k)
}
