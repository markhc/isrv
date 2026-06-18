// Package telemetry initializes OpenTelemetry tracing, metrics, and logging
// for the application and exposes the global instruments used throughout the
// codebase. All exporter configuration is read from the standard OTEL_*
// environment variables.
package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/markhc/isrv/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// InstrumentationName is the OpenTelemetry instrumentation scope name used
// for all spans and metrics emitted directly by this application's code (as
// opposed to the HTTP server spans from the FiberTracing middleware).
const InstrumentationName = "github.com/markhc/isrv"

// metricsHandler stores the Prometheus scrape handler installed by Setup so
// MetricsHandler can return it from any goroutine. Before Setup runs it
// returns a 503 handler, allowing callers to mount the route unconditionally.
var metricsHandler atomic.Value //nolint:gochecknoglobals // bridges Setup() and MetricsHandler() across packages.

// MetricsHandler returns the HTTP handler that serves the Prometheus scrape
// endpoint. Before Setup runs (or when telemetry is disabled) it returns a
// handler that responds with 503 Service Unavailable, so it remains safe to
// mount on a route at startup.
func MetricsHandler() http.Handler {
	if h, ok := metricsHandler.Load().(http.Handler); ok && h != nil {
		return h
	}

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "metrics endpoint not initialised", http.StatusServiceUnavailable)
	})
}

// Tracer returns the application tracer scoped to InstrumentationName. Use
// it to create spans inside handlers and other application code.
//
//nolint:ireturn // trace.Tracer is the OTel API contract; returning the concrete type would couple callers to the SDK
func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationName)
}

// ShutdownFunc flushes and shuts down all telemetry pipelines. It must be
// called before the process exits so buffered spans, metrics, and logs are
// delivered to the OTLP backend.
type ShutdownFunc func(context.Context) error

// Setup initialises the global OpenTelemetry tracer, meter, and logger
// providers and registers them as the OTel globals.
//
// Exporter configuration (endpoint, auth headers, protocol) is read entirely
// from the standard OTEL_* environment variables, keeping this code
// backend-agnostic. Notable variables:
//
//   - OTEL_EXPORTER_OTLP_ENDPOINT  – base URL of the OTLP receiver
//   - OTEL_EXPORTER_OTLP_HEADERS   – comma-separated key=value auth headers
//   - OTEL_SERVICE_NAME            – overrides the default service.name
//   - OTEL_RESOURCE_ATTRIBUTES     – additional resource key=value pairs
//   - OTEL_TRACES_SAMPLER          – trace sampler (default parentbased_always_on)
//   - OTEL_TRACES_SAMPLER_ARG      – sampler argument, e.g. 0.1 for 10% sampling
//
// buildVersion is used as the default value for the service.version resource
// attribute and can be overridden via OTEL_RESOURCE_ATTRIBUTES.
//
// When telemetry is disabled in cfg, Setup is a no-op and returns a no-op
// shutdown function. The caller must invoke the returned ShutdownFunc before
// process exit to flush buffered telemetry.
//
//nolint:funlen,cyclop // Exporter+provider+metric bootstrap is intentionally linear and unfolded.
func Setup(ctx context.Context, cfg models.TelemetryConfiguration, buildVersion string) (ShutdownFunc, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("isrv"),
			semconv.ServiceVersion(buildVersion),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithProcess(),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTel resource: %w", err)
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}

	// The sampler defaults to ParentBased(AlwaysSample). Set
	// OTEL_TRACES_SAMPLER (and OTEL_TRACES_SAMPLER_ARG) at runtime to switch
	// to ratio-based head sampling without a code change; the SDK reads
	// those env vars when no explicit WithSampler option is provided.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)

	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		_ = tp.Shutdown(ctx)

		return nil, fmt.Errorf("create metric exporter: %w", err)
	}

	promReader, err := promexporter.New(
		promexporter.WithRegisterer(prometheus.DefaultRegisterer),
	)
	if err != nil {
		_ = tp.Shutdown(ctx)

		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(append([]sdkmetric.Option{
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		sdkmetric.WithReader(promReader),
		sdkmetric.WithResource(res),
	}, metricViews()...)...)

	metricsHandler.Store(promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	))

	logExporter, err := otlploghttp.New(ctx)
	if err != nil {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)

		return nil, fmt.Errorf("create log exporter: %w", err)
	}

	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	global.SetLoggerProvider(lp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if err := InitMetrics(); err != nil {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		_ = lp.Shutdown(ctx)

		return nil, fmt.Errorf("init application metrics: %w", err)
	}

	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		_ = lp.Shutdown(ctx)

		return nil, fmt.Errorf("start runtime instrumentation: %w", err)
	}

	return func(ctx context.Context) error {
		var errs []error
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("trace provider shutdown: %w", err))
		}
		if err := mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider shutdown: %w", err))
		}
		if err := lp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("log provider shutdown: %w", err))
		}
		if len(errs) > 0 {
			return fmt.Errorf("telemetry shutdown errors: %v", errs)
		}

		return nil
	}, nil
}

// metricViews returns the explicit histogram bucket boundaries used by the
// application's custom instruments. Byte-size buckets cover typical upload
// sizes from a few KiB to several GiB; duration buckets target the
// millisecond-to-multi-second range of storage and cleanup operations.
func metricViews() []sdkmetric.Option {
	byteBuckets := []float64{
		1 << 10,       // 1 KiB
		16 << 10,      // 16 KiB
		256 << 10,     // 256 KiB
		1 << 20,       // 1 MiB
		8 << 20,       // 8 MiB
		64 << 20,      // 64 MiB
		256 << 20,     // 256 MiB
		1 << 30,       // 1 GiB
		4 * (1 << 30), // 4 GiB
	}

	durationBuckets := []float64{
		0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5,
		1, 2.5, 5, 10, 30, 60, 120, 300,
	}

	return []sdkmetric.Option{
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "isrv.upload.size_bytes"},
			sdkmetric.Stream{
				Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: byteBuckets},
			},
		)),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "isrv.storage.operation.duration"},
			sdkmetric.Stream{
				Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: durationBuckets},
			},
		)),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "isrv.cleanup.cycle.duration"},
			sdkmetric.Stream{
				Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: durationBuckets},
			},
		)),
	}
}
