package app

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"text/template"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/app/handlers"
	"github.com/markhc/isrv/internal/app/middleware"
	"github.com/markhc/isrv/internal/cleanup"
	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/favicon"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
)

// AppMiddleware bundles the Fiber middleware functions wired into the router.
type AppMiddleware struct {
	RequireToken fiber.Handler
	RateLimit    fiber.Handler
}

// Application bundles the Fiber handlers, middleware, and other dependencies
// wired into the router. It is constructed once by NewApplication and passed
// to SetupRoutes.
type Application struct {
	IndexHandler    fiber.Handler
	FaviconHandler  fiber.Handler
	DownloadHandler fiber.Handler
	UploadHandler   fiber.Handler
	DeleteHandler   fiber.Handler
	ExpireHandler   fiber.Handler
	NotFoundHandler fiber.Handler
	HealthzHandler  fiber.Handler
	ReadyzHandler   fiber.Handler
	StaticHandler   fiber.Handler

	MetricsHandler http.Handler

	Middleware AppMiddleware
	Debug      bool
}

//go:embed templates
var templatesFolderEmbedded embed.FS

//go:embed static
var staticFilesEmbedded embed.FS

// NewApplication wires together handlers and middleware from the supplied
// dependencies.
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
		HealthzHandler:  handlers.Healthz(),
		ReadyzHandler:   handlers.Readyz(db, stor),
		MetricsHandler:  telemetry.MetricsHandler(),

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
		a.StaticHandler = handlers.Static(staticFilesDir)
	}

	return a
}

// fiberErrorHandler converts handler-returned errors to JSON responses.
func fiberErrorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	msg := "internal server error"
	var fe *fiber.Error
	if errors.As(err, &fe) {
		code = fe.Code

		if fe.Code < fiber.StatusInternalServerError {
			msg = fe.Message
		}
	}

	logging.ErrorCtx(c.Context(), "request handler error",
		logging.Int("status", code),
		logging.Error(err),
	)

	return c.Status(code).JSON(map[string]string{"error": msg})
}

// StartApp initialises all dependencies, registers routes, and runs the
// Fiber server until ctx is cancelled (typically via signal.NotifyContext in
// the caller).
//
//nolint:funlen,cyclop
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
		// A missing favicon is non-fatal: the app simply serves none.
		logging.LogError("failed to fetch favicon", logging.String("url", config.FaviconURL), logging.Error(err))
	}

	application := NewApplication(ctx, config, dbInstance, storageClient, tmpl, faviconData, staticFilesDir)

	// BodyLimit enforces a hard upload cap before handler code runs;
	// derived from MaxFileSizeMB so the limit matches the upload handler.
	bodyLimit := config.MaxFileSizeMB * 1024 * 1024

	app := fiber.New(fiber.Config{
		ReadTimeout:      30 * time.Second,
		WriteTimeout:     30 * time.Second,
		IdleTimeout:      120 * time.Second,
		BodyLimit:        bodyLimit,
		ErrorHandler:     fiberErrorHandler,
		ProxyHeader:      fiber.HeaderXForwardedFor,
		TrustProxy:       len(config.TrustedProxies) > 0,
		TrustProxyConfig: fiber.TrustProxyConfig{Proxies: config.TrustedProxies},
	})

	SetupRoutes(app, application)

	logging.LogInfo(
		"starting webserver",
		logging.String("host", config.ServerHost),
		logging.Int("port", config.ServerPort),
	)

	addr := fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort)

	srvErr := make(chan error, 1)
	go func() {
		if err := app.Listen(addr, fiber.ListenConfig{DisableStartupMessage: true}); err != nil {
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

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
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
