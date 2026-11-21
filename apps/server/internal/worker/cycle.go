package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// instrumentation is the scope every worker span and metric is reported under.
const instrumentation = "github.com/vantigo-io/vantigo/server/internal/worker"

// The metric instruments are made once from the global provider. otel's
// globals delegate, so instruments made before telemetry.Setup installs a
// provider report through it once it does. The tracer is looked up per
// cycle, from whatever provider is installed then.
var (
	meter = otel.Meter(instrumentation)

	cyclesCounter, _ = meter.Int64Counter("vantigo.worker.cycles",
		metric.WithDescription("Worker cycles run, by worker and outcome (ok, error, panic)."),
		metric.WithUnit("{cycle}"))
	cycleDuration, _ = meter.Float64Histogram("vantigo.worker.cycle.duration",
		metric.WithDescription("How long one worker cycle took, by worker and outcome."),
		metric.WithUnit("s"))
	claimsCounter, _ = meter.Int64Counter("vantigo.worker.claims",
		metric.WithDescription("Rows a lease-taking worker claimed, by worker."),
		metric.WithUnit("{claim}"))
)

// Outcomes of a cycle, the outcome attribute of vantigo.worker.cycles.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
	OutcomePanic = "panic"
)

// cycle is one open cycle: its span, when it began, and whether it has been
// ended (by its end func or, after a panic, by the runner).
type cycle struct {
	name  string
	span  trace.Span
	start time.Time
	mu    sync.Mutex
	ended bool
}

func (c *cycle) end(outcome string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ended {
		return false
	}
	c.ended = true
	attrs := metric.WithAttributes(attribute.String("worker", c.name), attribute.String("outcome", outcome))
	cyclesCounter.Add(context.Background(), 1, attrs)
	cycleDuration.Record(context.Background(), time.Since(c.start).Seconds(), attrs)
	c.span.SetAttributes(attribute.String("vantigo.worker.outcome", outcome))
	c.span.End()
	return true
}

type cycleKey struct{}

// slot is where the runner can find the cycle its worker has open, so a
// panic that unwinds through the worker's loop ends that cycle as a panic.
type slot struct {
	mu   sync.Mutex
	open *cycle
}

type slotKey struct{}

// StartCycle opens one cycle of the worker named name (Kubernetes readiness
// KR21): a root span "worker <name>" on the returned context, which the
// cycle's work runs under so its queries and outbound calls join it. Call
// the returned func with the cycle's error once it is done; it records the
// span's status and the cycle metrics. An error that arrives because ctx was
// cancelled — the process stopping — counts as ok, as the workers' own loops
// do not log it either.
//
//	cycleCtx, endCycle := worker.StartCycle(ctx, w.Name())
//	n, err := w.RunCycle(cycleCtx)
//	endCycle(err)
func StartCycle(ctx context.Context, name string) (context.Context, func(error)) {
	cctx, span := otel.Tracer(instrumentation).Start(ctx, "worker "+name,
		trace.WithNewRoot(),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("vantigo.worker", name)))
	c := &cycle{name: name, span: span, start: time.Now()}
	if s, ok := ctx.Value(slotKey{}).(*slot); ok {
		s.mu.Lock()
		s.open = c
		s.mu.Unlock()
	}
	cctx = context.WithValue(cctx, cycleKey{}, c)
	return cctx, func(err error) {
		outcome := OutcomeOK
		if err != nil && ctx.Err() == nil {
			outcome = OutcomeError
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		c.end(outcome)
	}
}

// Claimed records that the cycle open on ctx claimed n rows under a lease
// (vantigo.worker.claims). Outside a cycle, or for n ≤ 0, it does nothing.
func Claimed(ctx context.Context, n int) {
	c, ok := ctx.Value(cycleKey{}).(*cycle)
	if !ok || n <= 0 {
		return
	}
	claimsCounter.Add(ctx, int64(n), metric.WithAttributes(attribute.String("worker", c.name)))
	c.span.AddEvent("claimed", trace.WithAttributes(attribute.Int("count", n)))
}

// withSlot gives ctx a slot the runner reads after a panic.
func withSlot(ctx context.Context) (context.Context, *slot) {
	s := &slot{}
	return context.WithValue(ctx, slotKey{}, s), s
}

// panicked ends the cycle open in s, if any, as a panic: an exception event
// with the panic value and stack, the span's status set to error.
func (s *slot) panicked(rec any, stack string) {
	s.mu.Lock()
	c := s.open
	s.mu.Unlock()
	if c == nil {
		return
	}
	c.span.AddEvent("exception", trace.WithAttributes(
		attribute.String("exception.type", "panic"),
		attribute.String("exception.message", fmt.Sprint(rec)),
		attribute.String("exception.stacktrace", stack),
	))
	c.span.SetStatus(codes.Error, "panic")
	c.end(OutcomePanic)
}
