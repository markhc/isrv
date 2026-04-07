package routes

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/markhc/isrv/internal/app"
)

// SetupRoutes registers all application routes and their associated handlers and middleware.
// It returns a configured chi.Mux instance ready to be used as an HTTP handler.
func SetupRoutes(a *app.Application) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.NotFound(a.NotFoundHandler)

	if a.IndexHandler != nil {
		r.Get("/", a.IndexHandler)
	}

	if a.FaviconHandler != nil {
		r.Get("/favicon.{format}", a.FaviconHandler)
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
