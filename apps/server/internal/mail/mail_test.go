package mail_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/config"
	"github.com/vantigo-io/vantigo/server/internal/mail"
)

// Ported from ApplicationEmailSenderTests.AddApplicationEmail_SelectsLoggingSender_WhenProviderIsLogging.
func TestNew_SelectsLogDriver(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	cfg := &config.Config{
		Env:  config.Development,
		Mail: config.MailConfig{Driver: "log"},
	}

	sender, err := mail.New(cfg, logger)
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	if err := sender.Send(context.Background(), mail.Message{To: "person@example.test"}); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "person@example.test") {
		t.Fatalf("the log driver was not selected: log output %q lacks the recipient", buf.String())
	}
}

// Ported from ApplicationEmailSenderTests.AddApplicationEmail_SelectsSmtpSender_WhenProviderIsSmtp.
func TestNew_SelectsSMTPDriver(t *testing.T) {
	cfg := &config.Config{
		Env: config.Production,
		Mail: config.MailConfig{
			Driver: "smtp",
			Host:   "smtp.example.test",
			Port:   587,
			From:   "noreply@example.test",
			TLS:    "starttls",
		},
	}

	sender, err := mail.New(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	if sender == nil {
		t.Fatal("New() sender = nil, want a Sender")
	}
}

func TestNew_RejectsUnknownDriver(t *testing.T) {
	cfg := &config.Config{Mail: config.MailConfig{Driver: "carrier-pigeon"}}
	if _, err := mail.New(cfg, slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("New() = nil error, want an error for an unknown driver")
	}
}
