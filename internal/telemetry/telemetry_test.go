package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// erroringMeter fails on the first instrument registration, exercising the
// error-wrapping path in registerMetrics. The embedded (nil) Meter is never
// reached because registerMetrics returns on the first failure.
type erroringMeter struct {
	metric.Meter
}

func (erroringMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("boom")
}

// scrape drives the Prometheus exposition endpoint and returns the response
// body. Collecting the body forces the SDK to run observable-gauge callbacks.
func scrape(t *testing.T) (int, string) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	MetricsHandler().ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, string(body)
}

func TestMetricsHandler(t *testing.T) {
	require.NotNil(t, MetricsHandler())

	status, body := scrape(t)

	assert.Equal(t, http.StatusOK, status)
	// The default Prometheus registry always carries the Go runtime and process
	// collectors, so the body is non-empty even before Setup runs.
	assert.NotEmpty(t, body)
}

func TestRegisterMetrics(t *testing.T) {
	// Build a real (non no-op) meter provider so registerMetrics binds against
	// concrete instruments without touching the process-global provider.
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	require.NoError(t, registerMetrics(mp.Meter(InstrumentationName)))

	// Every exported instrument var must be bound after a successful register.
	assert.NotNil(t, Uploads)
	assert.NotNil(t, UploadSize)
	assert.NotNil(t, Downloads)
	assert.NotNil(t, FilesDeleted)
	assert.NotNil(t, CleanupCycleDuration)
	assert.NotNil(t, CleanupFilesProcessed)
	assert.NotNil(t, RateLimitDecisions)
	assert.NotNil(t, StorageOpDuration)
}

func TestRegisterMetricsError(t *testing.T) {
	// registerMetrics rebinds package-global instrument vars as it goes, so a
	// failing register leaves them nil; restore no-op bindings afterwards to
	// keep the rest of the suite hermetic regardless of test order.
	t.Cleanup(func() { _ = InitMetrics() })

	err := registerMetrics(erroringMeter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "isrv.uploads")
}

func TestInitMetrics(t *testing.T) {
	// Rebinds against the current global provider; the no-op meter never errors.
	require.NoError(t, InitMetrics())
}

// TestSetup exercises the full pipeline: it installs the global meter provider,
// registers the blocklist gauge against it, verifies the gauge callback is
// observed via a Prometheus scrape, and finally shuts the pipeline down.
//
// Setup registers the Prometheus exporter with prometheus.DefaultRegisterer,
// which rejects duplicate registration, so this is the single Setup call in the
// package's test binary. Keeping the Setup-dependent assertions together makes
// the test order-independent.
func TestSetup(t *testing.T) {
	ctx := context.Background()

	// Point the OTLP metrics exporter at a local server that accepts every
	// export, so Setup takes the OTLP-reader branch and the periodic reader's
	// final flush on shutdown succeeds instead of hanging on an unreachable
	// collector.
	otlpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(otlpSrv.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", otlpSrv.URL+"/v1/metrics")

	shutdown, err := Setup(ctx, "v-test")
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	t.Run("RegisterBlocklistGauge is observed", func(t *testing.T) {
		const want = 42

		require.NoError(t, RegisterBlocklistGauge("http", func() int64 { return want }))

		status, body := scrape(t)
		assert.Equal(t, http.StatusOK, status)
		assert.True(t,
			strings.Contains(body, "isrv_ratelimit_blocklist_size"),
			"scrape body should expose the blocklist gauge:\n%s", body)
		assert.Contains(t, body, `limiter="http"`,
			"gauge should carry the limiter attribute")
		assert.Contains(t, body, "42")
	})

	t.Run("custom instruments are exposed after Setup", func(t *testing.T) {
		// InitMetrics (called by Setup) rebound the counters to the real
		// provider; recording now flows to the Prometheus registry.
		Uploads.Add(ctx, 1)

		_, body := scrape(t)
		assert.Contains(t, body, "isrv_uploads")
	})

	require.NoError(t, shutdown(ctx))
}
