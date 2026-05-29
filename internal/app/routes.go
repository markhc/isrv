package app

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/markhc/isrv/internal/logging"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"
)

// SetupRoutes registers all application routes and their associated handlers and middleware.
// It returns a configured chi.Mux instance ready to be used as an HTTP handler.
// The returned mux is wrapped with OpenTelemetry HTTP instrumentation that emits
// per-request traces and RED metrics (rate, errors, duration) to the configured backend.
//
//nolint:funlen
func SetupRoutes(a *Application) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// r.Use(middleware.Logger)

	// Request logger
	r.Use(logging.RequestLogger(&logging.RequestLoggerOptions{
		LogLevel:     zapcore.InfoLevel,
		RecoverPanic: true,
		SkipFunc: func(req *http.Request, respStatus int) bool {
			return req.Method == http.MethodOptions || respStatus == 404 || respStatus == 405
		},
	}))

	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	// After chi has resolved the route, update the active otelhttp span name and
	// http.route metric label to the matched route pattern (e.g. "GET /d/{id}").
	// Without this the outer otelhttp wrapper only sees the raw URL path.
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

	// Rate limited and protected routes
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

	// Wrap the entire mux with OTel HTTP instrumentation.
	// This emits a trace span and HTTP server metrics for every request,
	// providing RED metrics (rate, errors, duration) out of the box.
	return otelhttp.NewHandler(r, "isrv",
		otelhttp.WithMessageEvents(otelhttp.ReadEvents, otelhttp.WriteEvents),
	)
}
