package mail_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/mail"
)

const secretLink = "https://example.test/reset?token=temporary-secret"

// Ported from ApplicationEmailSenderTests.LoggingSender_NeverLogsSubjectOrBody
// (apps/identity/backend/Identity.Module.Tests/Services/ApplicationEmailSenderTests.cs).
func TestLog_NeverLogsSubjectOrBody(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sender := mail.NewLog(logger)

	body := "Use " + secretLink + " to continue."
	err := sender.Send(context.Background(), mail.Message{
		To:       "person@example.test",
		Subject:  "Reset your Vantigo password",
		TextBody: body,
	})
	if err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}

	out := buf.String()
	if !strings.Contains(out, "person@example.test") {
		t.Fatalf("log output %q does not contain the recipient", out)
	}
	if !strings.Contains(out, "message_id") {
		t.Fatalf("log output %q does not contain a message id", out)
	}
	if strings.Contains(out, "Reset your Vantigo password") {
		t.Fatalf("log output %q contains the subject", out)
	}
	if strings.Contains(out, body) || strings.Contains(out, secretLink) || strings.Contains(out, "token=") {
		t.Fatalf("log output %q contains the body or the link", out)
	}
}

// Ported from ApplicationEmailSenderTests.LoggingSender_FormattedLogDoesNotContainSecretOrLink.
func TestLog_FormattedLogDoesNotContainSecretOrLink(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sender := mail.NewLog(logger)

	body := "Use " + secretLink + " to continue."
	if err := sender.Send(context.Background(), mail.Message{
		To:       "person@example.test",
		Subject:  "Reset your Vantigo password",
		TextBody: body,
	}); err != nil {
		t.Fatalf("Send() = %v, want nil", err)
	}

	out := buf.String()
	if strings.Contains(out, secretLink) {
		t.Fatalf("log output %q contains the secret link", out)
	}
	if strings.Contains(out, "token=") {
		t.Fatalf("log output %q contains the token", out)
	}
}

// TestLog_RecordsOneEntryPerSend: the log driver writes one entry, naming
// the recipient, per message sent. It ports no .NET test; the two
// driver-selection tests it once cited are ported in mail_test.go
// (TestNew_SelectsLogDriver, TestNew_SelectsSMTPDriver).
func TestLog_RecordsOneEntryPerSend(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	sender := mail.NewLog(logger)

	for i := 0; i < 2; i++ {
		if err := sender.Send(context.Background(), mail.Message{To: "person@example.test"}); err != nil {
			t.Fatalf("Send() = %v, want nil", err)
		}
	}

	if n := strings.Count(buf.String(), "person@example.test"); n != 2 {
		t.Fatalf("log output has %d recipient entries, want 2", n)
	}
}
