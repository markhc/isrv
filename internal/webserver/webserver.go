package webserver

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

	"github.com/gorilla/mux"
	"github.com/markhc/isrv/internal/cleanup"
	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/webserver/favicon"
	"github.com/markhc/isrv/internal/webserver/handlers"
	"github.com/markhc/isrv/internal/webserver/router"
)

//go:embed templates
var templatesFolderEmbedded embed.FS

//go:embed static
var staticFilesEmbedded embed.FS

// Start initialises all dependencies, registers routes, and runs the HTTP server
// until an interrupt or termination signal is received.
//
//nolint:funlen
func Start(ctx context.Context) {
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

	muxRouter := createRouter(config, dbInstance, storageClient, tmpl, staticFilesDir, faviconData)

	logging.LogInfo(
		"starting webserver", logging.String("host", config.ServerHost), logging.Int("port", config.ServerPort))

	// Create the http server
	httpSrv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		Handler:      muxRouter,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start the server in a separate goroutine so that it doesn't block the main thread
	// so we can listen for shutdown signals and gracefully shut down the server when needed.
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.LogFatal("failed to start server", logging.Error(err))
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

func createRouter(
	config *models.Configuration,
	db database.Database,
	stor storage.Storage,
	tmpl *template.Template,
	staticFilesDir fs.FS,
	faviconData []byte,
) *mux.Router {
	muxRouter := mux.NewRouter()
	muxRouter.NotFoundHandler = handlers.NotFound(tmpl, config)

	r := router.NewRouter(muxRouter, config)

	if !config.DisableIndexPage {
		r.Handle("/", handlers.Index(tmpl, config)).
			Methods(http.MethodGet)
	}

	if !config.DisableUploadPage {
		r.Handle("/static/{file}", handlers.Static(staticFilesDir)).
			Methods(http.MethodGet)
	}

	if config.FaviconURL != "" && faviconData != nil {
		r.Handle("/favicon."+config.FaviconFormat, handlers.Favicon(faviconData, config.FaviconFormat)).
			Methods(http.MethodGet)
	}

	r.Handle("/d/{fileID}", handlers.Download(db, stor)).
		Methods(http.MethodGet).
		WithLogging()

	r.Handle("/d/{fileID}/{fileName}", handlers.Download(db, stor)).
		Methods(http.MethodGet).
		WithLogging()

	r.Handle("/", handlers.Upload(config, db, stor)).
		Methods(http.MethodPost).
		WithLogging().
		WithRateLimit()

	r.Build()

	return muxRouter
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
