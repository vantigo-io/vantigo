package communications

import (
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// This file is SV/CommunicationsMetrics.cs (communications inventory §18),
// ported whole — and it is deliberately the whole of it.
//
// **The inventory's closing sentence is binding** (`:1880`): .NET has FOUR
// unlabelled `Counter<long>`s, all on the outbox, and "there are **no**
// metrics for retention, attachment cleanup, object storage, SMTP latency,
// or the AI feature; and no histograms or gauges anywhere in the module."
// Every one of those is a tempting addition and every one of them would be a
// divergence rather than an improvement: an operator's dashboard built on
// this module must show what the .NET module showed. A fifth instrument
// belongs to whoever decides to add it on purpose, with the inventory
// updated to say so.
//
// The instruments carry NO attributes — no channel, no provider, no status
// dimension (`:1878`). .NET's counters take none, so a Go port that added a
// status attribute would produce a differently-shaped time series under the
// same metric name, which is worse than a different name.
//
// **Why OpenTelemetry and not something new.** internal/telemetry is this
// project's only observability wiring: it installs global OTel providers for
// whichever signals have an OTLP endpoint configured and leaves the no-op
// providers in place otherwise (telemetry.Setup). It is not a metrics
// *facility* of its own — there is no counter registry, no /metrics handler,
// no custom interface — so the platform's existing pattern for emitting a
// metric is exactly what telemetry_test.go itself does:
// `otel.Meter(name).Int64Counter(...)`. go.opentelemetry.io/otel and
// go.opentelemetry.io/otel/metric are already direct dependencies, so this
// adds no dependency at all, and a deployment with no OTLP endpoint pays the
// no-op provider's cost, which is none.

// meterName is the .NET Meter's name (`SV/CommunicationsMetrics.cs:8-10`,
// inventory `:1868`), which the .NET host exported by literal string
// (`HOST/Observability/VantigoTelemetry.cs:80`). Kept verbatim, dots and
// capitals included: an operator's existing alert rules are keyed on the
// metric names below, and those are scoped by this meter.
const meterName = "Vantigo.Communications"

// outboxMetrics is the module's four counters. They are held per worker
// rather than in a package-level var so a test can hand a worker its own
// MeterProvider and read exact values back, instead of asserting against a
// process-global instrument every other parallel test in the package is also
// incrementing.
type outboxMetrics struct {
	// possibleDuplicateSends is communications.outbox.possible_duplicate_sends
	// (`:18-20`, fired at `OutboxJobProcessor.cs:95`): a candidate job found
	// 'processing' with an expired lease AND a non-null delivery_attempted_at
	// — a crash between the external send and the completion commit. Counted
	// on the CANDIDATE, before the claim is confirmed, so a lost claim race
	// still counts (inventory §13.2).
	possibleDuplicateSends metric.Int64Counter
	// jobsCompleted is communications.outbox.jobs_completed (`:23-25`): +1 on
	// a successful send completion AND +1 on the nothing-sendable completion.
	// NOT incremented for a job cancelled by suppression (inventory §18).
	jobsCompleted metric.Int64Counter
	// jobsRetried is communications.outbox.jobs_retried (`:28-30`): +1 per
	// non-terminal send failure.
	jobsRetried metric.Int64Counter
	// jobsFailed is communications.outbox.jobs_failed (`:36-38`): +1 per
	// terminal failure. .NET's own doc comment is worth carrying over —
	// "Every increment is undelivered customer email; alert on this."
	jobsFailed metric.Int64Counter
}

// newOutboxMetrics builds the four counters from mp. A nil mp means the
// global provider, which is what production passes: internal/telemetry
// installs a real MeterProvider globally when an OTLP metrics endpoint is
// configured and leaves the no-op one in place when it is not.
//
// An instrument that fails to build becomes a no-op rather than an error:
// this is a background worker's telemetry, and a module that refuses to
// deliver mail because a counter could not be created would be trading a
// real outage for an observability gap.
func newOutboxMetrics(mp metric.MeterProvider) *outboxMetrics {
	meter := mp.Meter(meterName)
	return &outboxMetrics{
		possibleDuplicateSends: counter(meter, "communications.outbox.possible_duplicate_sends",
			"Outbox jobs re-claimed after a crash window in which the external send may already have happened."),
		jobsCompleted: counter(meter, "communications.outbox.jobs_completed",
			"Outbox jobs that reached the completed state."),
		jobsRetried: counter(meter, "communications.outbox.jobs_retried",
			"Outbox jobs whose send attempt failed and was scheduled for retry."),
		jobsFailed: counter(meter, "communications.outbox.jobs_failed",
			"Outbox jobs that terminally failed after exhausting their attempts."),
	}
}

// counter builds one Int64Counter, falling back to a no-op instrument rather
// than propagating an error (see newOutboxMetrics).
func counter(meter metric.Meter, name, description string) metric.Int64Counter {
	c, err := meter.Int64Counter(name, metric.WithDescription(description))
	if err != nil {
		return noop.Int64Counter{}
	}
	return c
}
