package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/app/handlers"
	"github.com/markhc/isrv/internal/app/middleware"
	"github.com/markhc/isrv/internal/cleanup"
	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/encryption"
	"github.com/markhc/isrv/internal/favicon"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
	"github.com/markhc/isrv/web"
)

// AppMiddleware bundles the Fiber middleware functions wired into the router.
type AppMiddleware struct {
	RequireToken   fiber.Handler
	RequireAdmin   fiber.Handler
	RateLimit      fiber.Handler
	AdminRateLimit fiber.Handler
}

// Application bundles the Fiber handlers, middleware, and other dependencies
// wired into the router. It is constructed once by NewApplication and passed
// to SetupRoutes.
type Application struct {
	FaviconHandler  fiber.Handler
	DownloadHandler fiber.Handler
	UploadHandler   fiber.Handler
	DeleteHandler   fiber.Handler
	ExpireHandler   fiber.Handler
	HealthzHandler  fiber.Handler
	ReadyzHandler   fiber.Handler
	SPAHandler      fiber.Handler

	AdminLoginHandler   fiber.Handler
	AdminLogoutHandler  fiber.Handler
	AdminSessionHandler fiber.Handler
	AdminListHandler    fiber.Handler
	AdminDeleteHandler  fiber.Handler
	AdminEnabled        bool

	MetricsHandler http.Handler

	Middleware AppMiddleware
	Debug      bool
}

// NewApplication wires together handlers and middleware from the supplied
// dependencies.
func NewApplication(
	ctx context.Context,
	config *models.Configuration,
	db database.Database,
	stor storage.Storage,
	enc *encryption.Manager,
	faviconData []byte,
) *Application {
	a := &Application{
		DownloadHandler: handlers.Download(db, stor, enc),
		UploadHandler:   handlers.Upload(config, db, stor, enc),
		DeleteHandler:   handlers.Delete(db, stor),
		ExpireHandler:   handlers.Expire(config, db),
		HealthzHandler:  handlers.Healthz(),
		ReadyzHandler:   handlers.Readyz(db, stor),
		MetricsHandler:  telemetry.MetricsHandler(),

		Middleware: AppMiddleware{
			RequireToken: middleware.RequireToken(db),
			RateLimit:    middleware.RateLimit(ctx, config.Security.RateLimit),
		},
	}

	a.Debug = config.DebugMode

	if config.Admin.Enabled() {
		a.AdminEnabled = true
		a.AdminLoginHandler = handlers.AdminLogin(config.Admin)
		a.AdminLogoutHandler = handlers.AdminLogout()
		a.AdminSessionHandler = handlers.AdminSession(config.Admin)
		a.AdminListHandler = handlers.AdminListFiles(db)
		a.AdminDeleteHandler = handlers.AdminDeleteFile(db, stor)
		a.Middleware.RequireAdmin = middleware.RequireAdmin(config.Admin)
		a.Middleware.AdminRateLimit = middleware.RateLimitFailedLogins(ctx, config.Admin)
	}

	if config.FaviconURL != "" && faviconData != nil {
		a.FaviconHandler = handlers.Favicon(faviconData, config.FaviconFormat)
	}

	if !config.DisableIndexPage {
		a.SPAHandler = handlers.SPA(web.DistDirFS, models.FrontendConfig{
			ServerName:        config.ServerName,
			DisableUploadPage: config.DisableUploadPage,
			MaxFileSizeMB:     config.MaxFileSizeMB,
			MinAgeDays:        config.MinAgeDays,
			MaxAgeDays:        config.MaxAgeDays,
			Version:           configuration.BuildVersion,
		})
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
// Fiber server until ctx is cancelled.
func StartApp(ctx context.Context) error {
	config := configuration.Get()

	storageClient, err := createStorage(ctx, config)
	if err != nil {
		return fmt.Errorf("initialise storage: %w", err)
	}

	encManager, err := encryption.NewManager(config.Encryption)
	if err != nil {
		return fmt.Errorf("initialise encryption: %w", err)
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

	cleanupService := cleanup.NewService(dbInstance, storageClient, config.Cleanup.Enabled, config.Cleanup.Interval)
	cancelCleanup := cleanupService.Start(ctx)

	faviconData, err := favicon.FetchFavicon(ctx, config.FaviconURL)
	if err != nil {
		// A missing favicon is non-fatal: the app simply serves none.
		logging.LogError("failed to fetch favicon", logging.String("url", config.FaviconURL), logging.Error(err))
	}

	application := NewApplication(ctx, config, dbInstance, storageClient, encManager, faviconData)

	// BodyLimit enforces a hard upload cap before handler code runs;
	// derived from MaxFileSizeMB so the limit matches the upload handler.
	bodyLimit := config.MaxFileSizeMB * 1024 * 1024

	validateIpAddresses := config.Security.ValidateIpAddresses
	if config.Security.RateLimit.Enabled && !validateIpAddresses {
		logging.LogDebug("rate limiting enabled but IP address validation disabled; enabling IP address validation")
		validateIpAddresses = true
	}

	app := fiber.New(fiber.Config{
		ReadTimeout:        30 * time.Second,
		WriteTimeout:       30 * time.Second,
		IdleTimeout:        120 * time.Second,
		BodyLimit:          bodyLimit,
		ErrorHandler:       fiberErrorHandler,
		ProxyHeader:        fiber.HeaderXForwardedFor,
		TrustProxy:         len(config.Security.TrustedProxies) > 0,
		TrustProxyConfig:   fiber.TrustProxyConfig{Proxies: config.Security.TrustedProxies},
		EnableIPValidation: validateIpAddresses,
		// Required so values stored via fiber.StoreInContext (e.g. the request
		// ID set by the requestid middleware) are reachable through
		// context.Context in logging.Ctx and the *Ctx helpers.
		PassLocalsToContext: true,
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
	case "postgres":
		dbInstance = database.NewPostgresDB(*config)
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
