package live

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// meterName scopes the live instruments (L16).
const meterName = "vantigo.live"

// metrics are the hub's instruments: two gauges, kept as up-down counters,
// and three counters.
type metrics struct {
	connections   metric.Int64UpDownCounter // vantigo.live.connections
	subscriptions metric.Int64UpDownCounter // vantigo.live.subscriptions
	notifications metric.Int64Counter       // vantigo.live.notifications: events the listener received
	resyncs       metric.Int64Counter       // vantigo.live.resyncs{reason}
	denied        metric.Int64Counter       // vantigo.live.denied{reason}

	docUpdates     metric.Int64Counter   // vantigo.live.doc_updates{result}
	docSeeds       metric.Int64Counter   // vantigo.live.doc_seeds{result}
	docCompactions metric.Int64Counter   // vantigo.live.doc_compactions{result}
	docDropped     metric.Int64Counter   // vantigo.live.doc_dropped{reason}
	docApplyErrors metric.Int64Counter   // vantigo.live.doc_apply_errors
	docReplayRows  metric.Int64Histogram // vantigo.live.doc_replay_rows
}

func newMetrics(mp metric.MeterProvider) *metrics {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(meterName)
	var m metrics
	var err error
	if m.connections, err = meter.Int64UpDownCounter("vantigo.live.connections", metric.WithDescription("Open live sockets on this hub.")); err != nil {
		m.connections, _ = noop.NewMeterProvider().Meter(meterName).Int64UpDownCounter("vantigo.live.connections")
	}
	if m.subscriptions, err = meter.Int64UpDownCounter("vantigo.live.subscriptions", metric.WithDescription("Topic subscriptions on this hub.")); err != nil {
		m.subscriptions, _ = noop.NewMeterProvider().Meter(meterName).Int64UpDownCounter("vantigo.live.subscriptions")
	}
	if m.notifications, err = meter.Int64Counter("vantigo.live.notifications", metric.WithDescription("Events the hub's listener received.")); err != nil {
		m.notifications, _ = noop.NewMeterProvider().Meter(meterName).Int64Counter("vantigo.live.notifications")
	}
	if m.resyncs, err = meter.Int64Counter("vantigo.live.resyncs", metric.WithDescription("Resync frames sent, by reason.")); err != nil {
		m.resyncs, _ = noop.NewMeterProvider().Meter(meterName).Int64Counter("vantigo.live.resyncs")
	}
	if m.denied, err = meter.Int64Counter("vantigo.live.denied", metric.WithDescription("Subscriptions denied, by reason.")); err != nil {
		m.denied, _ = noop.NewMeterProvider().Meter(meterName).Int64Counter("vantigo.live.denied")
	}
	counter := func(name, desc string) metric.Int64Counter {
		c, err := meter.Int64Counter(name, metric.WithDescription(desc))
		if err != nil {
			c, _ = noop.NewMeterProvider().Meter(meterName).Int64Counter(name)
		}
		return c
	}
	m.docUpdates = counter("vantigo.live.doc_updates", "Document updates, by result.")
	m.docSeeds = counter("vantigo.live.doc_seeds", "Document seeds, by result.")
	m.docCompactions = counter("vantigo.live.doc_compactions", "Document compactions, by result.")
	m.docDropped = counter("vantigo.live.doc_dropped", "Document frames dropped, by reason.")
	m.docApplyErrors = counter("vantigo.live.doc_apply_errors", "Updates a client could not apply, as it reported.")
	if m.docReplayRows, err = meter.Int64Histogram("vantigo.live.doc_replay_rows", metric.WithDescription("Rows sent in one document replay.")); err != nil {
		m.docReplayRows, _ = noop.NewMeterProvider().Meter(meterName).Int64Histogram("vantigo.live.doc_replay_rows")
	}
	return &m
}

func (m *metrics) docUpdate(result string) {
	m.docUpdates.Add(context.Background(), 1, metric.WithAttributes(attribute.String("result", result)))
}

func (m *metrics) docSeed(result string) {
	m.docSeeds.Add(context.Background(), 1, metric.WithAttributes(attribute.String("result", result)))
}

func (m *metrics) docCompaction(result string) {
	m.docCompactions.Add(context.Background(), 1, metric.WithAttributes(attribute.String("result", result)))
}

func (m *metrics) docDrop(reason string) {
	m.docDropped.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", reason)))
}

func (m *metrics) resync(reason string, n int64) {
	m.resyncs.Add(context.Background(), n, metric.WithAttributes(attribute.String("reason", reason)))
}

func (m *metrics) deny(reason string) {
	m.denied.Add(context.Background(), 1, metric.WithAttributes(attribute.String("reason", reason)))
}
