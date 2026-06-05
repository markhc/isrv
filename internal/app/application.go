package app

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
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
	RequireValidFileID func(http.Handler) http.Handler
	RequireToken       func(http.Handler) http.Handler
	RateLimit          func(http.Handler) http.Handler
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
		ExpireHandler:   handlers.Expire(config, db),
		NotFoundHandler: handlers.NotFound(tmpl, config),

		Middleware: AppMiddleware{
			RequireValidFileID: middleware.RequireValidFileID(db),
			RequireToken:       middleware.RequireToken(db),
			RateLimit:          middleware.RateLimit(ctx, config.RateLimit),
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

// StartApp initialises all dependencies, registers routes, and runs the HTTP
// server until the supplied context is cancelled (typically by SIGINT/SIGTERM
// installed by the caller via signal.NotifyContext).
//
// It returns a non-nil error when startup fails. Telemetry must already be
// initialised by the caller; logger/telemetry shutdown also remains the
// caller's responsibility.
//
//nolint:funlen,cyclop // linear init/run/shutdown sequence; splitting it further hurts readability
func StartApp(ctx context.Context) error {
	staticFilesDir, _ := fs.Sub(staticFilesEmbedded, "static")

	config := configuration.Get()

	storageClient, err := createStorage(ctx, config)
	if err != nil {
		return fmt.Errorf("initialise storage: %w", err)
	}

	dbInstance, err := createDb(config)
	if err != nil {
		return fmt.Errorf("initialise database: %w", err)
	}
	defer func() {
		if err := dbInstance.Close(); err != nil {
			logging.LogError("failed to close database connection", logging.Error(err))
		}
	}()

	tmpl, err := initializeTemplates(templatesFolderEmbedded)
	if err != nil {
		return fmt.Errorf("initialise templates: %w", err)
	}

	cleanupService := cleanup.NewService(dbInstance, storageClient, config.Cleanup.Enabled, config.Cleanup.Interval)
	cancelCleanup := cleanupService.Start(ctx)

	faviconData, err := favicon.FetchFavicon(ctx, config.FaviconURL)
	if err != nil {
		// Favicon fetch failure is non-fatal; the app simply serves no favicon.
		logging.LogError("failed to fetch favicon", logging.String("url", config.FaviconURL), logging.Error(err))
	}

	application := NewApplication(ctx, config, dbInstance, storageClient, tmpl, faviconData, staticFilesDir)

	logging.LogInfo(
		"starting webserver", logging.String("host", config.ServerHost), logging.Int("port", config.ServerPort))

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort),
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		Handler:           SetupRoutes(application),
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	srvErr := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
		close(srvErr)
	}()

	logging.LogInfo("server started successfully")

	var runErr error
	select {
	case <-ctx.Done():
		logging.LogInfo("shutting down server...")
	case err := <-srvErr:
		runErr = fmt.Errorf("http server: %w", err)
		logging.LogError("server failed", logging.Error(runErr))
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	if cancelCleanup != nil {
		cancelCleanup()
		cleanupService.Join()
	}

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logging.LogError("server forced to shutdown", logging.Error(err))
		runErr = errors.Join(runErr, fmt.Errorf("http server shutdown: %w", err))
	}

	return runErr
}

//nolint:ireturn
func createDb(config *models.Configuration) (database.Database, error) {
	var dbInstance database.Database

	switch config.Database.Type {
	case "sqlite":
		dbInstance = database.NewSQLiteDB(*config)
	default:
		return nil, fmt.Errorf("invalid database type %q", config.Database.Type)
	}

	if err := dbInstance.Connect(); err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	if err := dbInstance.Migrate(); err != nil {
		if closeErr := dbInstance.Close(); closeErr != nil {
			logging.LogError("failed to close database after migration error", logging.Error(closeErr))
		}

		return nil, fmt.Errorf("migrate database: %w", err)
	}

	return dbInstance, nil
}

//nolint:ireturn
func createStorage(ctx context.Context, config *models.Configuration) (storage.Storage, error) {
	switch config.Storage.Type {
	case "local":
		ls, err := storage.NewLocalStorage(config.Storage)
		if err != nil {
			return nil, fmt.Errorf("initialise local storage: %w", err)
		}

		return ls, nil
	case "s3":
		s3, err := storage.NewS3Storage(ctx, config.Storage)
		if err != nil {
			return nil, fmt.Errorf("initialise s3 storage: %w", err)
		}

		return s3, nil
	default:
		return nil, fmt.Errorf("invalid storage type %q", config.Storage.Type)
	}
}

func initializeTemplates(templatesFS embed.FS) (*template.Template, error) {
	templateFolder, err := template.New("").ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		logging.LogError("failed to initialize templates", logging.Error(err))

		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	return templateFolder, nil
}
