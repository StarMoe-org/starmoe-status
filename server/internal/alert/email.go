package alert

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/StarMoe-org/starmoe-status/server/internal/config"
)

// EmailNotifier sends alert emails via Resend HTTPS API or SMTPS/STARTTLS.
type EmailNotifier struct {
	from         string
	to           []string
	resendAPIKey string
	smtpHost     string
	smtpPort     int
	smtpUser     string
	smtpPass     string
	httpClient   *http.Client
}

// NewEmailNotifier constructs an EmailNotifier from Config.
func NewEmailNotifier(cfg *config.Config) *EmailNotifier {
	return &EmailNotifier{
		from:         cfg.EmailFrom,
		to:           cfg.EmailTo,
		resendAPIKey: cfg.ResendAPIKey,
		smtpHost:     cfg.SMTPHost,
		smtpPort:     cfg.SMTPPort,
		smtpUser:     cfg.SMTPUser,
		smtpPass:     cfg.SMTPPass,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether an email transport and recipients are configured.
func (e *EmailNotifier) Enabled() bool {
	if e == nil || e.from == "" || len(e.to) == 0 {
		return false
	}
	return e.resendAPIKey != "" || e.smtpHost != ""
}

// Send dispatches a plain-text alert email via Resend (preferred if set) or SMTP.
func (e *EmailNotifier) Send(ctx context.Context, subject, body string) error {
	if !e.Enabled() {
		return errors.New("email notifier not configured")
	}
	if e.resendAPIKey != "" {
		return e.sendResend(ctx, subject, body)
	}
	return e.sendSMTP(ctx, subject, body)
}

func (e *EmailNotifier) sendResend(ctx context.Context, subject, body string) error {
	payload := map[string]any{
		"from":    e.from,
		"to":      e.to,
		"subject": subject,
		"text":    body,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.resendAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("resend request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("resend status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

func (e *EmailNotifier) sendSMTP(ctx context.Context, subject, body string) error {
	addr := net.JoinHostPort(e.smtpHost, fmt.Sprintf("%d", e.smtpPort))
	tlsCfg := &tls.Config{
		ServerName: e.smtpHost,
		MinVersion: tls.VersionTLS12,
	}

	var client *smtp.Client
	var err error
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	if e.smtpPort == 465 {
		conn, dialErr := tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if dialErr != nil {
			return fmt.Errorf("smtps dial: %w", dialErr)
		}
		client, err = smtp.NewClient(conn, e.smtpHost)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("smtps client: %w", err)
		}
	} else {
		conn, dialErr := dialer.DialContext(ctx, "tcp", addr)
		if dialErr != nil {
			return fmt.Errorf("smtp dial: %w", dialErr)
		}
		client, err = smtp.NewClient(conn, e.smtpHost)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("smtp client: %w", err)
		}
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				_ = client.Close()
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	defer client.Close()

	if e.smtpUser != "" {
		auth := smtp.PlainAuth("", e.smtpUser, e.smtpPass, e.smtpHost)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	fromAddr := extractEmailAddress(e.from)
	if err := client.Mail(fromAddr); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, rcpt := range e.to {
		if err := client.Rcpt(extractEmailAddress(rcpt)); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		e.from, strings.Join(e.to, ", "), subject, body)
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func extractEmailAddress(s string) string {
	if i := strings.IndexByte(s, '<'); i >= 0 {
		if j := strings.IndexByte(s[i:], '>'); j > 0 {
			return strings.TrimSpace(s[i+1 : i+j])
		}
	}
	return strings.TrimSpace(s)
}
