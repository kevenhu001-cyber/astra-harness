package authsrv

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"net/url"
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

// SMTPMailer sends verification email via an SMTP server.
// Configure with SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASS / SMTP_FROM.
// SMTP_PROXY optionally routes the connection through an HTTP CONNECT proxy
// (needed on hosts whose egress firewall blocks SMTP ports).
type SMTPMailer struct {
	Host  string
	Port  string
	User  string
	Pass  string
	From  string
	Proxy string
}

func NewSMTPMailerFromEnv() *SMTPMailer {
	return &SMTPMailer{
		Host:  getenv("SMTP_HOST"),
		Port:  getenv("SMTP_PORT"),
		User:  getenv("SMTP_USER"),
		Pass:  getenv("SMTP_PASS"),
		From:  getenv("SMTP_FROM"),
		Proxy: getenv("SMTP_PROXY"),
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
	conn, err := m.dial(net.JoinHostPort(m.Host, m.Port))
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()

	var c *smtp.Client
	if m.Port == "465" {
		// Implicit TLS (common for 465): wrap the tunnel before SMTP hello.
		tconn := tls.Client(conn, &tls.Config{ServerName: m.Host})
		if err := tconn.Handshake(); err != nil {
			return fmt.Errorf("smtp tls: %w", err)
		}
		c, err = smtp.NewClient(tconn, m.Host)
	} else {
		// STARTTLS (587 and friends): plain SMTP hello first.
		c, err = smtp.NewClient(conn, m.Host)
		if err == nil {
			err = c.StartTLS(&tls.Config{ServerName: m.Host})
		}
	}
	if err != nil {
		return fmt.Errorf("smtp connect: %w", err)
	}
	defer c.Close()

	if err := c.Auth(smtp.PlainAuth("", m.User, m.Pass, m.Host)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail-from: %w", err)
	}
	if err := c.Rcpt(email); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	_ = c.Quit()
	return nil
}

// dial connects directly, or through an HTTP CONNECT proxy when SMTP_PROXY is
// set (e.g. http://127.0.0.1:7890).
func (m *SMTPMailer) dial(addr string) (net.Conn, error) {
	if m.Proxy == "" {
		return net.Dial("tcp", addr)
	}
	u, err := url.Parse(m.Proxy)
	if err != nil {
		return nil, fmt.Errorf("parse proxy: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	proxyAddr := u.Host
	if u.Port() == "" {
		proxyAddr = net.JoinHostPort(u.Hostname(), "8080")
	}
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\nProxy-Connection: Keep-Alive\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.Contains(status, " 200 ") {
		conn.Close()
		return nil, fmt.Errorf("proxy connect failed: %s", strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	return &bufferedConn{Conn: conn, r: br}, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func getenv(k string) string {
	return os.Getenv(k)
}
