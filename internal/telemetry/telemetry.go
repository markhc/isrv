package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// InstrumentationName is the OpenTelemetry instrumentation scope name used
// for all metrics emitted directly by this application's code.
const InstrumentationName = "github.com/markhc/isrv"

// MetricsHandler returns the HTTP handler that serves the Prometheus scrape
// endpoint. It reads from the default Prometheus registry, which Setup wires
// the OpenTelemetry metrics pipeline into; before Setup has run it serves
// only the standard Go process and runtime collectors.
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	)
}

// ShutdownFunc flushes and shuts down the metrics pipeline. It must be called
// before the process exits so buffered metrics are delivered to the OTLP
// backend, when one is configured.
type ShutdownFunc func(context.Context) error

// Setup initialises the global OpenTelemetry meter provider and registers it
// as the OTel global.
//
// This service deliberately emits metrics only. Traces and OTLP log export are
// not configured: as request-level records they would carry client IPs,
// filenames, and precise timestamps, undermining the service's privacy
// posture. Aggregate metrics reveal no per-request detail.
//
// The Prometheus scrape endpoint (see MetricsHandler) is always wired up.
// OTLP push is opt-in: metrics are additionally pushed to a collector only
// when OTEL_EXPORTER_OTLP_ENDPOINT is set. Exporter configuration (endpoint,
// auth headers, protocol) is read entirely from the standard OTEL_*
// environment variables, keeping this code backend-agnostic. Notable
// variables:
//
//   - OTEL_EXPORTER_OTLP_ENDPOINT  – base URL of the OTLP receiver
//   - OTEL_EXPORTER_OTLP_HEADERS   – comma-separated key=value auth headers
//   - OTEL_SERVICE_NAME            – overrides the default service.name
//   - OTEL_RESOURCE_ATTRIBUTES     – additional resource key=value pairs
//
// buildVersion is used as the default value for the service.version resource
// attribute and can be overridden via OTEL_RESOURCE_ATTRIBUTES.
func Setup(ctx context.Context, buildVersion string) (ShutdownFunc, error) {
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

	promReader, err := promexporter.New(
		promexporter.WithRegisterer(prometheus.DefaultRegisterer),
	)
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	opts := []sdkmetric.Option{
		sdkmetric.WithReader(promReader),
		sdkmetric.WithResource(res),
	}

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		metricExporter, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return nil, fmt.Errorf("create metric exporter: %w", err)
		}

		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)))
	}

	mp := sdkmetric.NewMeterProvider(append(opts, metricViews()...)...)

	otel.SetMeterProvider(mp)

	if err := InitMetrics(); err != nil {
		_ = mp.Shutdown(ctx)

		return nil, fmt.Errorf("init application metrics: %w", err)
	}

	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		_ = mp.Shutdown(ctx)

		return nil, fmt.Errorf("start runtime instrumentation: %w", err)
	}

	return func(ctx context.Context) error {
		if err := mp.Shutdown(ctx); err != nil {
			return fmt.Errorf("meter provider shutdown: %w", err)
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
