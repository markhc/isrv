package app

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/markhc/isrv/internal/logging"
	"go.uber.org/zap/zapcore"
)

// SetupRoutes registers all application routes and their associated handlers and middleware.
// It returns a configured chi.Mux instance ready to be used as an HTTP handler.
func SetupRoutes(a *Application) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// r.Use(middleware.Logger)

	// Request logger
	r.Use(logging.RequestLogger(&logging.RequestLoggerOptions{
		LogLevel:     zapcore.InfoLevel,
		RecoverPanic: true,
		SkipFunc: func(req *http.Request, respStatus int) bool {
			return respStatus == 404
		},
	}))

	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

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

	r.Group(func(r chi.Router) {
		r.Use(a.Middleware.RequireToken)
		r.Use(a.Middleware.RateLimit)

		r.Post("/", a.UploadHandler)
		r.Delete("/{id}", a.DeleteHandler)
		r.Patch("/{id}/expire", a.ExpireHandler)
	})

	if a.StaticFiles != nil {
		staticFS := http.FileServer(a.StaticFiles)
		r.Get("/static/*", http.StripPrefix("/static/", staticFS).ServeHTTP)
	}

	return r
}
