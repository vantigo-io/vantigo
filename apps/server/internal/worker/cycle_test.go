package worker

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// cycleWorker runs the cycles it is given, one per Run, then returns.
type cycleWorker struct {
	name   string
	cycles []func(context.Context) error
}

func (w *cycleWorker) Name() string            { return w.name }
func (w *cycleWorker) Interval() time.Duration { return time.Second }
func (w *cycleWorker) Run(ctx context.Context) error {
	for _, c := range w.cycles {
		cctx, end := StartCycle(ctx, w.name)
		end(c(cctx))
	}
	return nil
}

// The meter provider is installed once per test binary: the instruments are
// made from the global provider at init, and otel delegates them to the
// first provider installed.
var reader = func() *sdkmetric.ManualReader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

func cycleCount(t *testing.T, worker, outcome string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "vantigo.worker.cycles" {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				w, _ := dp.Attributes.Value(attribute.Key("worker"))
				o, _ := dp.Attributes.Value(attribute.Key("outcome"))
				if w.AsString() == worker && o.AsString() == outcome {
					return dp.Value
				}
			}
		}
	}
	return 0
}

func TestCycles_ASpanAndAMetricPerCycle(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(tracenoop.NewTracerProvider()) })

	r := NewRunner(slog.New(slog.DiscardHandler))
	r.Start(context.Background(), []Worker{
		&cycleWorker{name: "test.cycles", cycles: []func(context.Context) error{
			func(ctx context.Context) error { Claimed(ctx, 3); return nil },
			func(context.Context) error { return errors.New("the batch failed") },
			func(context.Context) error { panic("boom") },
		}},
	})
	if err := r.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}

	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("%d spans, want 3", len(spans))
	}
	for _, s := range spans {
		if s.Name() != "worker test.cycles" || s.Parent().IsValid() {
			t.Errorf("span %q, parent %v: want a root named after the worker", s.Name(), s.Parent())
		}
	}
	if spans[0].Status().Code == codes.Error || len(spans[0].Events()) != 1 || spans[0].Events()[0].Name != "claimed" {
		t.Errorf("ok cycle: status %v, events %v", spans[0].Status(), spans[0].Events())
	}
	if spans[1].Status().Code != codes.Error || spans[1].Status().Description != "the batch failed" {
		t.Errorf("failed cycle: status %v", spans[1].Status())
	}
	if ev := spans[2].Events(); spans[2].Status().Code != codes.Error || len(ev) != 1 || ev[0].Name != "exception" {
		t.Errorf("panicked cycle: status %v, events %v", spans[2].Status(), ev)
	}

	for outcome, want := range map[string]int64{OutcomeOK: 1, OutcomeError: 1, OutcomePanic: 1} {
		if got := cycleCount(t, "test.cycles", outcome); got != want {
			t.Errorf("vantigo.worker.cycles{outcome=%s} = %d, want %d", outcome, got, want)
		}
	}
}

func TestCycles_ACancelledCycleIsNoError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, end := StartCycle(ctx, "test.cancelled")
	end(context.Canceled)
	if got := cycleCount(t, "test.cancelled", OutcomeError); got != 0 {
		t.Errorf("a cycle the shutdown cut short counted as an error")
	}
	if got := cycleCount(t, "test.cancelled", OutcomeOK); got != 1 {
		t.Errorf("ok = %d, want 1", got)
	}
}
