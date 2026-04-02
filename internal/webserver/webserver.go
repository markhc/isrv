package webserver

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net"
	"net/http"
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

	faviconData, err := fetchFavicon(ctx, config.FaviconURL)
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
		router.Handle("/", srv.indexHandler()).Methods("GET")
	}

	if !config.DisableUploadPage {
		router.Handle("/static/{file}", srv.staticFilesHandler(staticFilesDir)).Methods("GET")
	}

	if config.FaviconURL != "" && faviconData != nil {
		router.Handle("/favicon."+config.FaviconFormat, srv.faviconHandler(faviconData)).Methods("GET")
	}

	router.Handle("/d/{fileID}", srv.loggingMiddleware(srv.downloadHandler())).Methods("GET")
	router.Handle("/d/{fileID}/{fileName}", srv.loggingMiddleware(srv.downloadHandler())).Methods("GET")
	router.Handle("/", srv.loggingMiddleware(srv.uploadHandler())).Methods("POST")

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

func (s *server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logging.LogInfo(
			"incoming request",
			logging.String("method", r.Method),
			logging.String("path", r.URL.Path),
			logging.String("ip_address", utils.GetIPAddress(r)))
		next.ServeHTTP(w, r)
	})
}

func initializeTemplates(templatesFS embed.FS) (*template.Template, error) {
	templateFolder, err := template.New("").ParseFS(templatesFS, "templates/*.tmpl")
	if err != nil {
		logging.LogError("failed to initialize templates", logging.Error(err))

		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	return templateFolder, nil
}

func fetchFavicon(ctx context.Context, url string) ([]byte, error) {
	// We enforce a max size of 4KiB for the favicon to prevent problems
	// This is mostly due to the fact that the favicon is fetched and stored in memory
	const maxFaviconSize = int64(4 * 1024) // 4 KiB

	if url == "" {
		return nil, nil
	}

	isHttpURL := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	isLocalFile := !isHttpURL || strings.HasPrefix(url, "file://")

	if isLocalFile {
		localPath := strings.TrimPrefix(url, "file://")

		return loadFaviconFromFile(localPath, maxFaviconSize)
	} else if isHttpURL {
		return loadFaviconFromUrl(ctx, url, maxFaviconSize)
	}

	return nil, fmt.Errorf("unsupported favicon URL: %s", url)
}

func loadFaviconFromUrl(ctx context.Context, url string, maxFaviconSize int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for favicon URL: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch favicon from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch favicon: received status code %d", resp.StatusCode)
	}

	if resp.ContentLength > maxFaviconSize {
		return nil, errors.New("favicon size exceeds the maximum allowed limit of 4KiB")
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read favicon data: %w", err)
	}

	logging.LogInfo("favicon fetched and saved successfully", logging.String("url", url))

	return data, nil
}

func loadFaviconFromFile(localPath string, maxFaviconSize int64) ([]byte, error) {
	fileInfo, err := os.Stat(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to access favicon file: %w", err)
	}

	if fileInfo.Size() > maxFaviconSize {
		return nil, errors.New("favicon file size exceeds the maximum allowed limit of 4KiB")
	}

	data, err := os.ReadFile(localPath) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("failed to read favicon from local file: %w", err)
	}

	logging.LogInfo("favicon loaded from local file successfully", logging.String("path", localPath))

	return data, nil
}

type errorResponse struct {
	Error string `json:"error"`
}

type uploadResponse struct {
	Status     string `json:"status"`
	Filename   string `json:"filename"`
	Expiration string `json:"expiration"`
}

// respondWithError sends a JSON error response and logs any write failures.
func (s *server) respondWithError(w http.ResponseWriter, code int, message string) {
	if err := setJsonResponse(w, code, errorResponse{Error: message}); err != nil {
		logging.LogError("failed to write error response", logging.Error(err))
	}
}

// respondWithSuccess sends a JSON success response and logs any write failures.
func (s *server) respondWithSuccess(w http.ResponseWriter, data any) {
	if err := setJsonResponse(w, http.StatusOK, data); err != nil {
		logging.LogError("failed to write success response", logging.Error(err))
	}
}

func setJsonResponse(w http.ResponseWriter, statusCode int, data any) error {
	w.Header().Set("Content-Type", "application/json")

	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON response: %w", err)
	}

	w.WriteHeader(statusCode)

	_, err = w.Write(b)
	if err != nil {
		return fmt.Errorf("failed to write JSON response: %w", err)
	}

	return nil
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
			s.respondWithError(w, http.StatusBadRequest, err.Error())

			return
		}
		defer file.Close()

		if err := s.validateFileSize(header); err != nil {
			s.respondWithError(w, http.StatusRequestEntityTooLarge, err.Error())

			return
		}

		ipAddress := utils.GetIPAddress(r)
		expiration := calculateExpirationTime(r, header.Size, s.config)

		logging.LogInfo("file upload requested",
			logging.String("filename", header.Filename),
			logging.Int64("size", header.Size),
			logging.TimeRFC3339("expiration", expiration),
			logging.String("ip_address", ipAddress),
		)

		fileURL, err := s.processUpload(r.Context(), file, header, expiration, ipAddress)
		if err != nil {
			logging.LogError("failed to process file upload", logging.Error(err))
			s.respondWithError(w, http.StatusInternalServerError, "failed to process upload")

			return
		}

		s.respondWithSuccess(w, uploadResponse{
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

func calculateExpirationTime(r *http.Request, fileSize int64, config *models.Configuration) time.Time {
	// Calculates the default expiration date for this file.
	// Expiration is based on file size, with larger files having shorter expiration times.
	// Expiration formula: min_age + (min_age - max_age) * pow((file_size / max_size - 1), 3)
	//
	// If a **shorter** time than the default is specified in the "expires" form field,
	// that time is used instead.
	maxSizeBytes := int64(config.MaxFileSizeMB * 1024 * 1024)
	minAge := int64(config.MinAgeDays * 24 * 3600 * 1000) // in milliseconds
	maxAge := int64(config.MaxAgeDays * 24 * 3600 * 1000) // in milliseconds

	defaultExpires := minAge + int64(float64(minAge-maxAge)*utils.Pow3(float64(fileSize)/float64(maxSizeBytes)-1))
	defaultExpiresTime := time.Now().Add(time.Duration(defaultExpires) * time.Millisecond)

	if expiresStr := r.FormValue("expires"); expiresStr != "" {
		if expiresTime, err := utils.ParseExpiresForm(expiresStr); err == nil {
			if expiresTime.Before(defaultExpiresTime) {
				return expiresTime
			}
		}
	}

	return defaultExpiresTime
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

	return s.config.ServerURL + "/d/" + fileID + "/" + header.Filename, nil
}
