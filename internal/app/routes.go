package app

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/pprof"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/telemetry"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// infraEndpointPrefixes lists URL path prefixes for endpoints that should
// not produce HTTP server spans. They are high-frequency and add no
// operational value to tracing output.
//
//nolint:gochecknoglobals
var infraEndpointPrefixes = []string{
	"/healthz",
	"/readyz",
	"/metrics",
	"/debug/",
	"/static/",
}

// SetupRoutes registers all application routes, handlers, and middleware on
// the supplied fiber.App. Tracing is applied globally and skips the high-
// volume infra endpoints listed in infraEndpointPrefixes.
//
//nolint:funlen
func SetupRoutes(app *fiber.App, a *Application) {
	// Tracing first so it captures even early aborts.
	app.Use(telemetry.FiberTracing("isrv", infraEndpointPrefixes))

	// Request logger.
	app.Use(logging.RequestLogger(&logging.RequestLoggerOptions{
		SkipFunc: func(c fiber.Ctx, _ int) bool {
			return c.Method() == fiber.MethodOptions
		},
	}))

	// Panic recovery that records the panic on the active span before
	// allowing Fiber's recover middleware to translate it to a 500.
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c fiber.Ctx, e any) {
			span := trace.SpanFromContext(c.Context())
			span.SetStatus(codes.Error, "panic")
			logging.ErrorCtx(c.Context(), "request handler panic",
				logging.Any("panic", e),
			)
		},
	}))

	// 404 handler — registered via the app's NotFound mechanism below.
	if a.FaviconHandler != nil {
		app.Get("/favicon.:format", a.FaviconHandler)
	}

	app.Get("/d/:id", a.DownloadHandler)
	app.Get("/d/:id/:filename", a.DownloadHandler)

	// Rate-limited group.
	rateLimited := app.Group("", a.Middleware.RateLimit)
	rateLimited.Post("/", a.UploadHandler)
	rateLimited.Delete("/:id", a.Middleware.RequireToken, a.DeleteHandler)
	rateLimited.Patch("/:id/expire", a.Middleware.RequireToken, a.ExpireHandler)

	// Operational endpoints.
	if a.HealthzHandler != nil {
		app.Get("/healthz", a.HealthzHandler)
	}
	if a.ReadyzHandler != nil {
		app.Get("/readyz", a.ReadyzHandler)
	}
	if a.MetricsHandler != nil {
		app.Get("/metrics", adaptor.HTTPHandler(a.MetricsHandler))
	}

	// pprof — only mounted when DebugMode is enabled.
	if a.Debug {
		app.Use("/debug/pprof", pprof.New())
	}

	// SPA catch-all: serves index.html for any unmatched path so that
	// client-side routing works. Must be registered last.
	if a.SPAHandler != nil {
		app.Use(a.SPAHandler)
	}
}

// hasPathPrefix reports whether path begins with any of prefixes.
//
//nolint:unused // kept for parity with the legacy chi implementation
func hasPathPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// Ensure the net/http import keeps a use; HTTPHandler accepts http.Handler.
var _ http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
