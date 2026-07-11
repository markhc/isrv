package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// Application metric instruments. They are bound to the global Meter at
// package load time, which returns no-op instruments before a MeterProvider
// has been installed. Once Setup installs the real MeterProvider, InitMetrics
// rebinds these vars to instruments backed by that provider.
//
// All instruments are safe for concurrent use; recording on a no-op
// instrument is a side-effect-free no-op, useful for unit tests that do not
// call Setup.
var (
	Uploads               metric.Int64Counter
	UploadSize            metric.Int64Histogram
	Downloads             metric.Int64Counter
	FilesDeleted          metric.Int64Counter
	CleanupCycleDuration  metric.Float64Histogram
	CleanupFilesProcessed metric.Int64Counter
	RateLimitDecisions    metric.Int64Counter
	StorageOpDuration     metric.Float64Histogram
	ClusterRedisErrors    metric.Int64Counter
)

// init binds the application metric vars to no-op instruments via the global Meter
//
//nolint:gochecknoinits
func init() {
	if err := registerMetrics(otel.Meter(InstrumentationName)); err != nil {
		// The no-op meter never returns an error; the panic guards against a
		// future regression but is otherwise unreachable.
		panic(fmt.Sprintf("telemetry: bind no-op metric instruments: %v", err))
	}
}

// InitMetrics rebinds the application metric instruments against the global
// MeterProvider. It must be called once after Setup has installed the provider
// so that subsequent recordings flow to the configured backend.
func InitMetrics() error {
	if err := registerMetrics(otel.Meter(InstrumentationName)); err != nil {
		return fmt.Errorf("rebind metric instruments: %w", err)
	}

	return nil
}

func registerMetrics(meter metric.Meter) error {
	var err error

	if Uploads, err = meter.Int64Counter(
		"isrv.uploads",
		metric.WithDescription("Number of upload attempts, by result and storage backend."),
		metric.WithUnit("{upload}"),
	); err != nil {
		return fmt.Errorf("register isrv.uploads: %w", err)
	}

	if UploadSize, err = meter.Int64Histogram(
		"isrv.upload.size_bytes",
		metric.WithDescription("Size of successfully uploaded files."),
		metric.WithUnit("By"),
	); err != nil {
		return fmt.Errorf("register isrv.upload.size_bytes: %w", err)
	}

	if Downloads, err = meter.Int64Counter(
		"isrv.downloads",
		metric.WithDescription("Number of download attempts, by result and storage backend."),
		metric.WithUnit("{download}"),
	); err != nil {
		return fmt.Errorf("register isrv.downloads: %w", err)
	}

	if FilesDeleted, err = meter.Int64Counter(
		"isrv.files.deleted",
		metric.WithDescription("Number of files removed from storage, by source and result."),
		metric.WithUnit("{file}"),
	); err != nil {
		return fmt.Errorf("register isrv.files.deleted: %w", err)
	}

	if CleanupCycleDuration, err = meter.Float64Histogram(
		"isrv.cleanup.cycle.duration",
		metric.WithDescription("Duration of a single cleanup cycle, by result."),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("register isrv.cleanup.cycle.duration: %w", err)
	}

	if CleanupFilesProcessed, err = meter.Int64Counter(
		"isrv.cleanup.files_processed",
		metric.WithDescription("Number of expired files processed by the cleanup service, by result."),
		metric.WithUnit("{file}"),
	); err != nil {
		return fmt.Errorf("register isrv.cleanup.files_processed: %w", err)
	}

	if RateLimitDecisions, err = meter.Int64Counter(
		"isrv.ratelimit.decisions",
		metric.WithDescription("Rate-limiter decisions, by decision kind."),
		metric.WithUnit("{decision}"),
	); err != nil {
		return fmt.Errorf("register isrv.ratelimit.decisions: %w", err)
	}

	if ClusterRedisErrors, err = meter.Int64Counter(
		"isrv.cluster.redis.errors",
		metric.WithDescription(
			"Redis coordination failures, by operation; each one is a decision degraded to the memory fallback."),
		metric.WithUnit("{error}"),
	); err != nil {
		return fmt.Errorf("register isrv.cluster.redis.errors: %w", err)
	}

	if StorageOpDuration, err = meter.Float64Histogram(
		"isrv.storage.operation.duration",
		metric.WithDescription("Duration of storage backend operations."),
		metric.WithUnit("s"),
	); err != nil {
		return fmt.Errorf("register isrv.storage.operation.duration: %w", err)
	}

	return nil
}

// RegisterBlocklistGauge installs an ObservableGauge that reports the current
// rate-limit blocklist size on every metric collection cycle. The supplied
// callback is invoked from the metric SDK and must be safe for concurrent use.
//
// It is a no-op if the meter provider has not been registered yet.
func RegisterBlocklistGauge(observe func() int64) error {
	meter := otel.Meter(InstrumentationName)

	gauge, err := meter.Int64ObservableGauge(
		"isrv.ratelimit.blocklist.size",
		metric.WithDescription("Number of IP addresses currently in the rate-limit blocklist."),
		metric.WithUnit("{ip}"),
	)
	if err != nil {
		return fmt.Errorf("register isrv.ratelimit.blocklist.size: %w", err)
	}

	if _, err := meter.RegisterCallback(
		func(_ context.Context, obs metric.Observer) error {
			obs.ObserveInt64(gauge, observe())

			return nil
		},
		gauge,
	); err != nil {
		return fmt.Errorf("register blocklist gauge callback: %w", err)
	}

	return nil
}
