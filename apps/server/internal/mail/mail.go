// Package mail is the application mail port: identity's invitation and
// password-reset mails, and communications' mail, all go through Sender
// rather than talking to an SMTP server directly.
//
// New picks the driver from config: "smtp" (smtp.go) dials out through
// go-mail with a DNS-rebinding guard (guard.go); "log" (log.go) is a
// development-only stand-in that records that a mail was queued without
// ever logging its contents; Fake (fake.go) is for tests.
package mail

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vantigo-io/vantigo/server/internal/config"
)

// Message is an application mail: a recipient, a subject, and a plain-text
// body. Identity uses it for invitations and password resets; those bodies
// carry bearer links, which is why the log driver never logs them.
type Message struct {
	To       string
	Subject  string
	TextBody string
}

// Sender delivers a Message. Its implementations are NewSMTP, NewLog and
// Fake.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// New picks the mail driver named by cfg.Mail.Driver: "smtp" dials out with
// NewSMTP, "log" writes to logger with NewLog. Config validation already
// guarantees "log" appears only in development.
func New(cfg *config.Config, logger *slog.Logger) (Sender, error) {
	switch cfg.Mail.Driver {
	case "smtp":
		return NewSMTP(cfg.Mail)
	case "log":
		return NewLog(logger), nil
	default:
		return nil, fmt.Errorf("mail: unknown driver %q", cfg.Mail.Driver)
	}
}
