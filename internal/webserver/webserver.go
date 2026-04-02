package webserver

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/gorilla/mux"
	"github.com/markhc/isrv/internal/cleanup"
	"github.com/markhc/isrv/internal/configuration"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/headers"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/utils"
	"github.com/markhc/isrv/internal/webserver/favicon"
	"github.com/markhc/isrv/internal/webserver/middleware"
)

//go:embed templates
var templatesFolderEmbedded embed.FS

//go:embed static
var staticFilesEmbedded embed.FS

// server holds the dependencies and state for HTTP handlers.
type server struct {
	config    *models.Configuration
	db        database.Database
	storage   storage.Storage
	templates *template.Template
}

// newServer creates a new server with the given dependencies.
func newServer(config *models.Configuration, db database.Database, stor storage.Storage) (*server, error) {
	tmpl, err := initializeTemplates(templatesFolderEmbedded)
	if err != nil {
		return nil, err
	}

	return &server{
		config:    config,
		db:        db,
		storage:   stor,
		templates: tmpl,
	}, nil
}

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

	srv, err := newServer(config, dbInstance, storageClient)
	if err != nil {
		logging.LogFatal("failed to initialise server", logging.Error(err))
	}

	cleanupService := cleanup.NewService(dbInstance, storageClient, config.Cleanup.Enabled, config.Cleanup.Interval)
	cancelCleanup := cleanupService.Start(ctx)

	faviconData, err := favicon.FetchFavicon(ctx, config.FaviconURL)
	if err != nil {
		logging.LogError("failed to fetch favicon", logging.String("url", config.FaviconURL), logging.Error(err))
	}

	router := createRouter(srv, config, staticFilesDir, faviconData)

	logging.LogInfo(
		"starting webserver", logging.String("host", config.ServerHost), logging.Int("port", config.ServerPort))

	// Create the http server
	httpSrv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		Handler:      router,
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

func createRouter(srv *server, config *models.Configuration, staticFilesDir fs.FS, faviconData []byte) *mux.Router {
	router := mux.NewRouter()
	router.NotFoundHandler = srv.handler404()

	if !config.DisableIndexPage {
		router.Handle("/", srv.indexHandler()).Methods(http.MethodGet)
	}

	if !config.DisableUploadPage {
		router.Handle(
			"/static/{file}",
			srv.staticFilesHandler(staticFilesDir),
		).Methods(http.MethodGet)
	}

	if config.FaviconURL != "" && faviconData != nil {
		router.Handle(
			"/favicon."+config.FaviconFormat,
			srv.faviconHandler(faviconData),
		).Methods(http.MethodGet)
	}

	router.Handle(
		"/d/{fileID}",
		middleware.WithRequestLogging(srv.downloadHandler()),
	).Methods(http.MethodGet)

	router.Handle(
		"/d/{fileID}/{fileName}",
		middleware.WithRequestLogging(srv.downloadHandler()),
	).Methods(http.MethodGet)

	router.Handle(
		"/",
		middleware.WithRequestLogging(srv.uploadHandler()),
	).Methods(http.MethodPost)

	return router
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

func (s *server) handler404() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)

		data := struct {
			Config *models.Configuration
		}{
			Config: s.config,
		}

		err := s.templates.ExecuteTemplate(w, "notfound", data)
		if err != nil {
			logging.LogError("failed to execute template", logging.Error(err))
		}
	}
}

func (s *server) indexHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logging.LogDebug("indexHandler", logging.String("path", r.URL.Path))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		data := struct {
			Config *models.Configuration
		}{
			Config: s.config,
		}

		err := s.templates.ExecuteTemplate(w, "index", data)
		if err != nil {
			logging.LogError("failed to execute template", logging.Error(err))
		}
	}
}

func (s *server) faviconHandler(faviconData []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers.AddCacheHeader(w)
		headers.SetContentType(w, "image/"+s.config.FaviconFormat)
		_, err := w.Write(faviconData)
		if err != nil {
			logging.LogError("failed to write favicon response", logging.Error(err))
		}
	}
}

func (s *server) staticFilesHandler(staticFilesDir fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logging.LogDebug("staticFilesHandler", logging.String("path", r.URL.Path))

		vars := mux.Vars(r)
		file := vars["file"]

		if strings.Contains(file, "..") {
			http.Error(w, "Invalid file path", http.StatusBadRequest)

			return
		}

		headers.AddCacheHeader(w)

		http.StripPrefix("/static/", http.FileServer(http.FS(staticFilesDir))).ServeHTTP(w, r)
	}
}

func (s *server) downloadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		fileID := vars["fileID"]
		fileName := ""

		if name, ok := vars["fileName"]; ok {
			fileName = name
		}

		logging.LogDebug(
			"serving file",
			logging.String("file_id", fileID),
			logging.String("file_name", fileName),
			logging.String("path", r.URL.Path))

		// Update file metrics
		err := s.db.OnFileDownload(fileID)
		if err != nil {
			logging.LogError("failed to update file metrics", logging.Error(err))
		}

		metadata, _ := s.db.GetFileMetadata(fileID) // Fetch metadata to set appropriate headers

		s.storage.ServeFile(w, r, fileID, fileName, metadata, true, true)
	}
}

func (s *server) uploadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		file, header, err := s.validateUploadRequest(r)
		if err != nil {
			utils.RespondWithError(w, http.StatusBadRequest, err.Error())

			return
		}
		defer file.Close()

		if err := s.validateFileSize(header); err != nil {
			utils.RespondWithError(w, http.StatusRequestEntityTooLarge, err.Error())

			return
		}

		ipAddress := utils.GetIPAddress(r)
		expiration := utils.CalculateExpirationTime(r, header.Size, s.config)

		logging.LogInfo("file upload requested",
			logging.String("filename", header.Filename),
			logging.Int64("size", header.Size),
			logging.TimeRFC3339("expiration", expiration),
			logging.String("ip_address", ipAddress),
		)

		fileURL, err := s.processUpload(r.Context(), file, header, expiration, ipAddress)
		if err != nil {
			logging.LogError("failed to process file upload", logging.Error(err))
			utils.RespondWithError(w, http.StatusInternalServerError, "failed to process upload")

			return
		}

		utils.RespondWithSuccess(w, struct {
			Status     string `json:"status"`
			Filename   string `json:"filename"`
			Expiration string `json:"expiration"`
		}{
			Status:     "success",
			Filename:   fileURL,
			Expiration: expiration.Format(time.RFC3339),
		})
	}
}

// validateUploadRequest extracts and validates the uploaded file.
func (s *server) validateUploadRequest(r *http.Request) (multipart.File, *multipart.FileHeader, error) {
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, nil, errors.New("multipart form 'file' field is missing")
	}

	return file, header, nil
}

// validateFileSize checks if the uploaded file size is within limits.
func (s *server) validateFileSize(header *multipart.FileHeader) error {
	maxSizeBytes := int64(s.config.MaxFileSizeMB * 1024 * 1024)
	if header.Size > maxSizeBytes {
		return fmt.Errorf("file size exceeds the maximum allowed limit of %d MB", s.config.MaxFileSizeMB)
	}

	return nil
}

func (s *server) processUpload(
	ctx context.Context,
	file multipart.File,
	header *multipart.FileHeader,
	expiration time.Time,
	ipAddress string,
) (
	string,
	error,
) {
	logging.LogInfo("processing uploaded file: " + header.Filename)

	fileID := utils.GenerateRandomString(s.config.RandomIDLength)

	path, err := s.storage.SaveFileUpload(ctx, fileID, file, header)
	if err != nil {
		logging.LogError("failed to save uploaded file", logging.Error(err))

		return "", fmt.Errorf("failed to save uploaded file: %w", err)
	}

	logging.LogInfo("file uploaded successfully", logging.String("file_id", fileID), logging.String("path", path))

	err = s.db.OnFileUpload(fileID, header, expiration, ipAddress)
	if err != nil {
		logging.LogError("failed to update file metrics", logging.Error(err))
	}

	// URL encode the filename to prevent abuse
	safeFilename := url.PathEscape(header.Filename)

	return s.config.ServerURL + "/d/" + fileID + "/" + safeFilename, nil
}
