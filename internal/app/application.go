package app

import (
	"context"
	"io/fs"
	"net/http"
	"text/template"

	"github.com/markhc/isrv/internal/app/handlers"
	"github.com/markhc/isrv/internal/app/middleware"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
)

// AppMiddleware holds the middleware functions used by the application.
// Each field wraps an http.Handler and returns a new one.
type AppMiddleware struct {
	RequireToken func(http.Handler) http.Handler
	RateLimit    func(http.Handler) http.Handler
}

// Application is the central type that holds all HTTP handler fields and
// middleware. It is constructed once by New and passed to routes.SetupRoutes.
type Application struct {
	IndexHandler    http.HandlerFunc
	FaviconHandler  http.HandlerFunc
	DownloadHandler http.HandlerFunc
	UploadHandler   http.HandlerFunc
	DeleteHandler   http.HandlerFunc
	ExpireHandler   http.HandlerFunc
	NotFoundHandler http.HandlerFunc

	Middleware  AppMiddleware
	StaticFiles http.FileSystem
}

// NewApplication constructs an Application by wiring handler maker funcs and middleware
// constructors. All fallible initialization (DB, storage, templates, favicon)
// must be completed by the caller before invoking NewApplication.
func NewApplication(
	ctx context.Context,
	config *models.Configuration,
	db database.Database,
	stor storage.Storage,
	tmpl *template.Template,
	faviconData []byte,
	staticFilesDir fs.FS,
) *Application {
	a := &Application{
		DownloadHandler: handlers.Download(db, stor),
		UploadHandler:   handlers.Upload(config, db, stor),
		DeleteHandler:   handlers.Delete(db, stor),
		ExpireHandler:   handlers.Expire(db),
		NotFoundHandler: handlers.NotFound(tmpl, config),

		Middleware: AppMiddleware{
			RequireToken: middleware.RequireToken(db),
			RateLimit:    middleware.RateLimit(ctx, config.RateLimit),
		},
	}

	if !config.DisableIndexPage {
		a.IndexHandler = handlers.Index(tmpl, config)
	}

	if config.FaviconURL != "" && faviconData != nil {
		a.FaviconHandler = handlers.Favicon(faviconData, config.FaviconFormat)
	}

	if !config.DisableUploadPage {
		a.StaticFiles = http.FS(staticFilesDir)
	}

	return a
}
