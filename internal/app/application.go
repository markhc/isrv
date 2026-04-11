package app

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/template"
	"time"

	"github.com/markhc/isrv/internal/app/handlers"
	"github.com/markhc/isrv/internal/app/middleware"
	"github.com/markhc/isrv/internal/cleanup"
	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/favicon"
	"github.com/markhc/isrv/internal/logging"
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
	Debug       bool
}

//go:embed templates
var templatesFolderEmbedded embed.FS

//go:embed static
var staticFilesEmbedded embed.FS

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

	a.Debug = config.DebugMode

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

// StartApp initialises all dependencies, registers routes, and runs the HTTP server
// until an interrupt or termination signal is received.
//
//nolint:funlen
func StartApp(ctx context.Context) {
	staticFilesDir, _ := fs.Sub(staticFilesEmbedded, "static")

	config := configuration.Get()
	storageClient := createStorage(ctx, config)
	dbInstance := createDb(config)

	defer func() {
		if err := dbInstance.Close(); err != nil {
			logging.LogError("failed to close database connection", logging.Error(err))
		}
	}()

	tmpl, err := initializeTemplates(templatesFolderEmbedded)
	if err != nil {
		logging.LogFatal("failed to initialise server", logging.Error(err))
	}

	cleanupService := cleanup.NewService(dbInstance, storageClient, config.Cleanup.Enabled, config.Cleanup.Interval)
	cancelCleanup := cleanupService.Start(ctx)

	faviconData, err := favicon.FetchFavicon(ctx, config.FaviconURL)
	if err != nil {
		logging.LogError("failed to fetch favicon", logging.String("url", config.FaviconURL), logging.Error(err))
	}

	application := NewApplication(ctx, config, dbInstance, storageClient, tmpl, faviconData, staticFilesDir)

	logging.LogInfo(
		"starting webserver", logging.String("host", config.ServerHost), logging.Int("port", config.ServerPort))

	httpSrv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		Handler:      SetupRoutes(application),
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.LogError("failed to start server", logging.Error(err))
			quit <- syscall.SIGTERM
		}
	}()

	logging.LogInfo("server started successfully")

	<-quit
	logging.LogInfo("shutting down server...")

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if cancelCleanup != nil {
		cancelCleanup()
		cleanupService.Join()
	}

	if err := httpSrv.Shutdown(ctx); err != nil {
		logging.LogError("server forced to shutdown", logging.Error(err))
	}
}

//nolint:ireturn
func createDb(config *models.Configuration) database.Database {
	var dbInstance database.Database

	switch config.Database.Type {
	case "sqlite":
		dbInstance = database.NewSQLiteDB(*config)
	default:
		logging.LogFatal("invalid database type", logging.String("type", config.Database.Type))
	}

	err := dbInstance.Connect()
	if err != nil {
		logging.LogFatal("failed to connect to database", logging.Error(err))
	}

	err = dbInstance.Migrate()
	if err != nil {
		dbInstance.Close()
		logging.LogFatal("failed to migrate database", logging.Error(err))
	}

	return dbInstance
}

//nolint:ireturn
func createStorage(ctx context.Context, config *models.Configuration) storage.Storage {
	var storageClient storage.Storage

	switch config.Storage.Type {
	case "local":
		storageClient = storage.NewLocalStorage(config.Storage)
	case "s3":
		storageClient = storage.NewS3Storage(ctx, config.Storage)
	default:
		logging.LogFatal("invalid storage type", logging.String("type", config.Storage.Type))
	}

	return storageClient
}

func initializeTemplates(templatesFS embed.FS) (*template.Template, error) {
	templateFolder, err := template.New("").ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		logging.LogError("failed to initialize templates", logging.Error(err))

		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	return templateFolder, nil
}
