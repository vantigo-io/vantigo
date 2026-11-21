package telemetry

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/trace"
)

// NewLogger returns the process logger: JSON lines to w at level and, when
// the logs signal is exported, the same records to OpenTelemetry through the
// slog bridge (which uses the global LoggerProvider Setup installed).
//
// Every JSON line carries tenant when tenantID is set (Kubernetes readiness
// KR18): a cluster's log agent reads stdout and never sees the OTLP
// resource. A record logged with a context inside a span carries its
// trace_id and span_id (KR22), on the JSON line and — the SDK takes them from
// the same context — on the OTLP record.
func NewLogger(w io.Writer, level slog.Level, exportLogs bool, tenantID string) *slog.Logger {
	var h slog.Handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	if tenantID != "" {
		h = h.WithAttrs([]slog.Attr{slog.String("tenant", tenantID)})
	}
	h = traceIDs{h}
	if exportLogs {
		h = fanout{min: level, handlers: []slog.Handler{h, otelslog.NewHandler("vantigo")}}
	}
	return slog.New(h)
}

// traceIDs adds trace_id and span_id to a record whose context carries a
// valid span, unless the record already names a trace_id itself (the
// request log does, from httpx.TraceID).
type traceIDs struct{ slog.Handler }

func (t traceIDs) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		has := false
		r.Attrs(func(a slog.Attr) bool {
			has = a.Key == "trace_id"
			return !has
		})
		if !has {
			r = r.Clone()
			r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
		}
	}
	return t.Handler.Handle(ctx, r)
}

func (t traceIDs) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceIDs{t.Handler.WithAttrs(attrs)}
}

func (t traceIDs) WithGroup(name string) slog.Handler { return traceIDs{t.Handler.WithGroup(name)} }

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
