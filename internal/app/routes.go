package app

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/markhc/isrv/internal/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"
)

// infraEndpointPrefixes lists URL path prefixes for endpoints that should
// not produce HTTP server spans or RED metrics. They are high-frequency and
// add no operational value to tracing/metrics output.
//
//nolint:gochecknoglobals // immutable list shared between SetupRoutes and the otelhttp filter.
var infraEndpointPrefixes = []string{
	"/healthz",
	"/readyz",
	"/metrics",
	"/debug/",
	"/static/",
}

// otelhttpFilter reports whether a request should be traced. Spans are
// dropped for the high-volume infra endpoints listed in infraEndpointPrefixes.
func otelhttpFilter(r *http.Request) bool {
	for _, prefix := range infraEndpointPrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return false
		}
	}

	return true
}

// SetupRoutes registers all application routes, handlers, and middleware on
// a chi.Mux and returns the configured http.Handler. The returned handler is
// wrapped with OpenTelemetry HTTP instrumentation that emits per-request
// traces and RED metrics (rate, errors, duration). Infra endpoints listed in
// infraEndpointPrefixes are filtered out to avoid noise.
//
//nolint:funlen,cyclop // linear route registration; splitting hides the mux layout.
func SetupRoutes(a *Application) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)

	// Request logger. Panic recovery lives in the dedicated recoverer below so
	// panics can be attached to the active OpenTelemetry span. OPTIONS noise
	// is dropped outright; 404/405 are demoted to debug by getLogLevel so
	// they remain visible only when the operator turns the sink to debug.
	r.Use(logging.RequestLogger(&logging.RequestLoggerOptions{
		LogLevel: zapcore.DebugLevel,
		SkipFunc: func(req *http.Request, _ int) bool {
			return req.Method == http.MethodOptions
		},
	}))

	r.Use(spanAwareRecoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// Once chi has resolved the route, rewrite the active otelhttp span name
	// and http.route metric label to use the matched route pattern (e.g.
	// "GET /d/{id}"). Without this the outer otelhttp wrapper only sees the
	// raw URL path.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if routeCtx := chi.RouteContext(r.Context()); routeCtx != nil {
				if pattern := routeCtx.RoutePattern(); pattern != "" {
					spanName := r.Method + " " + pattern
					trace.SpanFromContext(r.Context()).SetName(spanName)

					if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
						labeler.Add(attribute.String("http.route", pattern))
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	})

	r.NotFound(a.NotFoundHandler)

	if a.IndexHandler != nil {
		r.Get("/", a.IndexHandler)
	} else {
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}

	if a.FaviconHandler != nil {
		r.Get("/favicon.{format}", a.FaviconHandler)
	} else {
		r.Get("/favicon.{format}", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}

	r.Get("/d/{id}", a.DownloadHandler)
	r.Get("/d/{id}/{filename}", a.DownloadHandler)

	// Rate-limited and protected routes.
	r.Group(func(r chi.Router) {
		r.Use(a.Middleware.RateLimit)

		r.Post("/", a.UploadHandler)

		r.Group(func(r chi.Router) {
			r.Use(a.Middleware.RequireValidFileID)
			r.Use(a.Middleware.RequireToken)

			r.Delete("/{id}", a.DeleteHandler)
			r.Patch("/{id}/expire", a.ExpireHandler)
		})
	})

	if a.StaticFiles != nil {
		staticFS := http.FileServer(a.StaticFiles)
		r.Get("/static/*", http.StripPrefix("/static/", staticFS).ServeHTTP)
	}

	// Operational endpoints are intentionally registered outside the
	// authentication and rate-limit groups so orchestrators and scrapers can
	// always reach them.
	if a.HealthzHandler != nil {
		r.Get("/healthz", a.HealthzHandler)
	}
	if a.ReadyzHandler != nil {
		r.Get("/readyz", a.ReadyzHandler)
	}
	if a.MetricsHandler != nil {
		r.Method(http.MethodGet, "/metrics", a.MetricsHandler)
	}

	// pprof is only mounted when DebugMode is enabled. It exposes process
	// internals (heap, goroutines, mutex traces) and must not be reachable
	// in untrusted environments without an upstream auth layer.
	if a.Debug {
		r.Mount("/debug", middleware.Profiler())
	}

	// Wrap the mux with OTel HTTP instrumentation. This emits a trace span
	// and HTTP server metrics for every request, providing RED metrics
	// (rate, errors, duration) out of the box. Infra endpoints are filtered
	// out by otelhttpFilter to avoid trace and metric noise.
	return otelhttp.NewHandler(r, "isrv",
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
		otelhttp.WithFilter(otelhttpFilter),
	)
}

// spanAwareRecoverer is a panic-recovery middleware that records the panic
// on the active OpenTelemetry span (so it surfaces in the trace backend)
// and emits a structured error log before responding with 500. It re-panics
// on http.ErrAbortHandler so the server can complete its abort handling.
func spanAwareRecoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		defer func() {
			rvr := recover()
			if rvr == nil {
				return
			}

			//nolint:errorlint // http.ErrAbortHandler is the documented sentinel value to identity-compare against.
			if rvr == http.ErrAbortHandler {
				panic(rvr)
			}

			err := fmt.Errorf("panic: %v", rvr)

			span := trace.SpanFromContext(ctx)
			span.RecordError(err, trace.WithStackTrace(true))
			span.SetStatus(codes.Error, "panic")

			logging.ErrorCtx(ctx, "request handler panic",
				logging.Error(err),
				logging.String("stack", string(debug.Stack())),
			)

			if r.Header.Get("Connection") != "Upgrade" {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
