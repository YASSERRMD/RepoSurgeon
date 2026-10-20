// Package telemetry instruments the RepoSurgeon API with OpenTelemetry spans
// and metrics: request latency, error rates, queue age, model usage and cost.
// When telemetry is disabled the provider is a no-op so the rest of the code
// can use it unconditionally.
package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// meterName and tracerName identify the RepoSurgeon instrumentation scope.
const (
	meterName  = "reposurgeon/meter"
	tracerName = "reposurgeon/tracer"
)

// Provider holds the OpenTelemetry tracer and meter providers.
type Provider struct {
	Tracer trace.TracerProvider
	Meter  otelmetric.MeterProvider
}

// noopProvider is returned when telemetry is disabled.
var noopProvider = Provider{
	Tracer: tracenoop.NewTracerProvider(),
	Meter:  metricnoop.NewMeterProvider(),
}

// NewProvider builds a telemetry provider. When enabled is false it returns a
// no-op provider so the rest of the code can use it unconditionally.
func NewProvider(enabled bool) Provider {
	if !enabled {
		return noopProvider
	}
	tracerProvider := sdktrace.NewTracerProvider()
	meterProvider := sdkmetric.NewMeterProvider()
	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	return Provider{Tracer: tracerProvider, Meter: meterProvider}
}

// Instruments are the named metric instruments the API records.
type Instruments struct {
	Requests       otelmetric.Int64Counter
	RequestErrors  otelmetric.Int64Counter
	RequestLatency otelmetric.Float64Histogram
	ModelTokens    otelmetric.Int64Counter
	ModelCost      otelmetric.Float64Counter
	QueueAge       otelmetric.Int64Gauge
}

// NewInstruments creates the metric instruments bound to a meter.
func NewInstruments(meter otelmetric.Meter) (*Instruments, error) {
	i := &Instruments{}
	var err error
	i.Requests, err = meter.Int64Counter("reposurgeon_requests_total",
		otelmetric.WithDescription("Total number of HTTP requests by path and status"),
	)
	if err != nil {
		return nil, err
	}
	i.RequestErrors, err = meter.Int64Counter("reposurgeon_request_errors_total",
		otelmetric.WithDescription("Total number of failed HTTP requests"),
	)
	if err != nil {
		return nil, err
	}
	i.RequestLatency, err = meter.Float64Histogram("reposurgeon_request_duration_seconds",
		otelmetric.WithDescription("HTTP request latency in seconds"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}
	i.ModelTokens, err = meter.Int64Counter("reposurgeon_model_tokens_total",
		otelmetric.WithDescription("Total model tokens by model and kind"),
	)
	if err != nil {
		return nil, err
	}
	i.ModelCost, err = meter.Float64Counter("reposurgeon_model_cost_usd_total",
		otelmetric.WithDescription("Total model cost in USD"),
	)
	if err != nil {
		return nil, err
	}
	i.QueueAge, err = meter.Int64Gauge("reposurgeon_queue_age_seconds",
		otelmetric.WithDescription("Age of the oldest queued run in seconds"),
	)
	if err != nil {
		return nil, err
	}
	return i, nil
}

// SpanAttrs returns common span attributes for an HTTP request.
func SpanAttrs(method, path string, status int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("http.method", method),
		attribute.String("http.route", path),
		attribute.Int("http.status_code", status),
	}
}

// StartSpan starts a child span of the current context's span.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return otel.Tracer(tracerName).Start(ctx, name, opts...)
}

// RecordRequest records request count, latency and errors against the
// instruments. status is the HTTP status code; latency is the elapsed time.
func RecordRequest(i *Instruments, ctx context.Context, method, path string, status int, latency time.Duration) {
	attrs := []attribute.KeyValue{
		attribute.String("http.method", method),
		attribute.String("http.route", path),
		attribute.Int("http.status_code", status),
	}
	i.Requests.Add(ctx, 1, otelmetric.WithAttributeSet(attribute.NewSet(attrs...)))
	if status >= 400 {
		i.RequestErrors.Add(ctx, 1, otelmetric.WithAttributeSet(attribute.NewSet(attrs...)))
	}
	i.RequestLatency.Record(ctx, latency.Seconds(), otelmetric.WithAttributeSet(attribute.NewSet(attrs...)))
}
