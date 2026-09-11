package mail

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// logSender is the development-only driver: it never sends anything, only
// records that a mail was queued. It logs the recipient and a random id —
// never the subject, body, or any link the body may contain, since
// invitation and password-reset mails carry bearer links there. Ported from
// LoggingApplicationEmailSender
// (apps/identity/backend/Identity.Module/Services/ApplicationEmail.cs).
type logSender struct {
	logger *slog.Logger
}

// NewLog returns a Sender that logs only the recipient of a queued mail and
// a random correlation id.
func NewLog(logger *slog.Logger) Sender {
	return &logSender{logger: logger}
}

func (s *logSender) Send(ctx context.Context, m Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "application mail queued",
		slog.String("recipient", m.To),
		slog.String("message_id", uuid.NewString()),
	)
	return nil
}
