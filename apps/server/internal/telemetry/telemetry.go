// Package telemetry wires OpenTelemetry: one OTLP/HTTP exporter per signal
// that has an endpoint configured, the slog bridge for logs, and HTTP server
// instrumentation. The SDK reads the standard OTEL_* variables itself, which
// is the one exception to "only cmd/vantigo reads the environment".
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"

	otelruntime "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Options describe the process for the telemetry resource.
type Options struct {
	Version     string
	Environment string
}

// Signals reports which signals are being exported.
type Signals struct {
	Traces, Metrics, Logs bool
}

// Setup installs a global provider for every signal with an OTLP endpoint and
// returns a shutdown func that flushes them. With nothing configured it
// installs nothing: the global no-op providers stay, and so does the cost of
// using them — none.
func Setup(ctx context.Context, o Options) (Signals, func(context.Context) error, error) {
	var signals Signals
	var shutdowns []func(context.Context) error
	shutdown := func(ctx context.Context) error {
		var errs []error
		for i := len(shutdowns) - 1; i >= 0; i-- {
			errs = append(errs, shutdowns[i](ctx))
		}
		return errors.Join(errs...)
	}
	fail := func(err error) (Signals, func(context.Context) error, error) {
		_ = shutdown(ctx)
		return Signals{}, func(context.Context) error { return nil }, err
	}

	if os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return signals, shutdown, nil
	}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"} {
		if p := os.Getenv(name); p != "" && p != "http/protobuf" {
			return fail(fmt.Errorf("telemetry: %s=%q is not supported; use http/protobuf", name, p))
		}
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", "vantigo"),
		attribute.String("service.version", o.Version),
		attribute.String("deployment.environment.name", o.Environment),
	))
	if err != nil {
		return fail(fmt.Errorf("telemetry: resource: %w", err))
	}

	if exporting("TRACES") {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return fail(fmt.Errorf("telemetry: trace exporter: %w", err))
		}
		tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))
		shutdowns = append(shutdowns, tp.Shutdown)
		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
		signals.Traces = true
	}
	if exporting("METRICS") {
		exp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return fail(fmt.Errorf("telemetry: metric exporter: %w", err))
		}
		mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)), sdkmetric.WithResource(res))
		shutdowns = append(shutdowns, mp.Shutdown)
		otel.SetMeterProvider(mp)
		if err := otelruntime.Start(otelruntime.WithMeterProvider(mp)); err != nil {
			return fail(fmt.Errorf("telemetry: runtime metrics: %w", err))
		}
		signals.Metrics = true
	}
	if exporting("LOGS") {
		exp, err := otlploghttp.New(ctx)
		if err != nil {
			return fail(fmt.Errorf("telemetry: log exporter: %w", err))
		}
		lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)), sdklog.WithResource(res))
		shutdowns = append(shutdowns, lp.Shutdown)
		global.SetLoggerProvider(lp)
		signals.Logs = true
	}
	return signals, shutdown, nil
}

// exporting reports whether signal (TRACES, METRICS, LOGS) has an endpoint.
func exporting(signal string) bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
}
