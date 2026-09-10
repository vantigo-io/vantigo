package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
)

// NewLogger returns the process logger: JSON lines to w at level and, when
// the logs signal is exported, the same records to OpenTelemetry through the
// slog bridge (which uses the global LoggerProvider Setup installed).
func NewLogger(w io.Writer, level slog.Level, exportLogs bool) *slog.Logger {
	var h slog.Handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	if exportLogs {
		h = fanout{min: level, handlers: []slog.Handler{h, otelslog.NewHandler("vantigo")}}
	}
	return slog.New(h)
}

// fanout sends every record at or above min to each of its handlers.
type fanout struct {
	min      slog.Level
	handlers []slog.Handler
}

func (f fanout) Enabled(_ context.Context, level slog.Level) bool { return level >= f.min }

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return fanout{min: f.min, handlers: hs}
}

func (f fanout) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return fanout{min: f.min, handlers: hs}
}
