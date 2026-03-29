package webserver

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"mime/multipart"
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
var templateFolder *template.Template

//go:embed static
var staticFilesEmbedded embed.FS

var storageClient storage.Storage
var dbInstance database.Database

func Start() {
	config := configuration.Get()
	initializeTemplates(templatesFolderEmbedded)

	staticFilesDir, _ := fs.Sub(staticFilesEmbedded, "static")

	switch config.Storage.Type {
	case "local":
		storageClient = storage.NewLocalStorage(config.Storage)
	case "s3":
		storageClient = storage.NewS3Storage(config.Storage)
	default:
		logging.LogFatal("Invalid storage type", logging.String("type", config.Storage.Type))
	}

	switch config.Database.Type {
	case "sqlite":
		dbInstance = database.NewSQLiteDB(*config)
	// case "postgres":
	// 	db = database.NewPostgresDB(*config)
	default:
		logging.LogFatal("Invalid database type", logging.String("type", config.Database.Type))
	}

	err := dbInstance.Connect()
	if err != nil {
		logging.LogFatal("Failed to connect to database", logging.Error(err))
	}

	defer dbInstance.Close()

	err = dbInstance.Migrate()
	if err != nil {
		logging.LogFatal("Failed to migrate database", logging.Error(err))
	}

	// Parse cleanup interval from config
	cleanupInterval, err := time.ParseDuration(config.Cleanup.Interval)
	if err != nil {
		logging.LogFatal("Invalid cleanup interval", logging.String("interval", config.Cleanup.Interval), logging.Error(err))
	}

	// Initialize and start cleanup service
	cleanupService := cleanup.NewService(dbInstance, storageClient, config.Cleanup.Enabled, cleanupInterval)
	cleanupService.Start()

	router := mux.NewRouter()
	router.NotFoundHandler = http.HandlerFunc(handler404)

	router.Handle("/", http.HandlerFunc(indexHandler)).
		Methods("GET")
	router.Handle("/static/{file}", staticFilesHandler(staticFilesDir)).
		Methods("GET")
	router.Handle("/d/{fileID}", loggingMiddleware(downloadHandler(config))).
		Methods("GET")
	router.Handle("/d/{fileID}/{fileName}", loggingMiddleware(downloadHandler(config))).
		Methods("GET")
	router.Handle("/", loggingMiddleware(uploadHandler(config))).
		Methods("POST")

	logging.LogInfo("Starting webserver", logging.String("host", config.ServerHost), logging.Int("port", config.ServerPort))
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.ServerHost, config.ServerPort),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		Handler:      router,
	}

	// Channel to listen for interrupt signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.LogFatal("Failed to start server", logging.Error(err))
		}
	}()

	logging.LogInfo("Server started successfully")

	// Wait for interrupt signal
	<-quit
	logging.LogInfo("Shutting down server...")

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop cleanup service
	cleanupService.Stop()

	// Attempt graceful shutdown of HTTP server
	if err := srv.Shutdown(ctx); err != nil {
		logging.LogError("Server forced to shutdown", logging.Error(err))
	}

	logging.LogInfo("Server shutdown complete")
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logging.LogInfo("incoming request", logging.String("method", r.Method), logging.String("path", r.URL.Path), logging.String("ip_address", utils.GetIPAddress(r)))
		next.ServeHTTP(w, r)
	})
}

func initializeTemplates(templatesFS embed.FS) {
	var err error

	templateFolder, err = template.New("").ParseFS(templatesFS, "templates/*.tmpl")

	if err != nil {
		logging.LogError("Failed to initialize templates")
		panic(err)
	}
}

func setJsonResponse(w http.ResponseWriter, statusCode int, data string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	w.Write([]byte(data))
}

func handler404(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)

	data := struct {
		Config *models.Configuration
	}{
		Config: configuration.Get(),
	}

	err := templateFolder.ExecuteTemplate(w, "notfound", data)
	if err != nil {
		logging.LogError("Failed to execute template: " + err.Error())
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	logging.LogDebug("indexHandler", logging.String("path", r.URL.Path))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := struct {
		Config *models.Configuration
	}{
		Config: configuration.Get(),
	}

	err := templateFolder.ExecuteTemplate(w, "index", data)
	if err != nil {
		logging.LogError("Failed to execute template: " + err.Error())
	}
}

func staticFilesHandler(staticFilesDir fs.FS) http.HandlerFunc {
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

func downloadHandler(_ *models.Configuration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		fileID := vars["fileID"]
		fileName := ""

		if name, ok := vars["fileName"]; ok {
			fileName = name
		}

		logging.LogDebug("Serving file", logging.String("file_id", fileID), logging.String("file_name", fileName), logging.String("path", r.URL.Path))

		// Update file metrics
		dbInstance.OnFileDownload(fileID)

		storageClient.ServeFile(w, r, fileID, fileName, true, true)
	}
}

func uploadHandler(config *models.Configuration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			setJsonResponse(w, http.StatusBadRequest, "Multipart form 'file' field is missing")
			return
		}
		defer file.Close()

		// Check file size
		if header.Size > int64(config.MaxFileSizeMB*1024*1024) {
			setJsonResponse(w, http.StatusRequestEntityTooLarge, "File size exceeds the maximum allowed limit of "+fmt.Sprintf("%d MB", config.MaxFileSizeMB))
			return
		}

		ipAddress := utils.GetIPAddress(r)
		expiration := calculateExpirationTime(r, header.Size, config)
		logging.LogInfo("File upload requested", logging.String("filename", header.Filename), logging.Int64("size", header.Size), logging.String("expiration", expiration.String()), logging.String("ip_address", ipAddress))

		fileURL, err := processUpload(config, file, header, expiration, ipAddress)
		if err != nil {
			setJsonResponse(w, http.StatusInternalServerError, "Failed to process upload")
			return
		}

		response := `{"status": "success", "filename": "` + fileURL + `", "expiration": "` + expiration.Format(time.RFC3339) + `"}`
		setJsonResponse(w, http.StatusOK, response)
	}
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

	if expiresStr := r.FormValue("expires"); expiresStr != "" {
		if expiresInt, err := utils.ParseExpiresForm(expiresStr); err == nil {
			if expiresInt < defaultExpires {
				defaultExpires = expiresInt
			}
		}
	}

	return time.Now().Add(time.Duration(defaultExpires) * time.Millisecond)
}

func processUpload(config *models.Configuration, file multipart.File, handler *multipart.FileHeader, expiration time.Time, ipAddress string) (string, error) {
	logging.LogInfo("Processing uploaded file: " + handler.Filename)

	fileID := handler.Filename
	fileID = utils.GenerateRandomString(config.RandomIDLength)

	path, err := storageClient.SaveFileUpload(fileID, file)
	if err != nil {
		logging.LogError("Failed to save uploaded file", logging.Error(err))
		return "", err
	}

	logging.LogInfo("File uploaded successfully", logging.String("file_id", fileID), logging.String("path", path))

	dbInstance.OnFileUpload(fileID, handler.Filename, handler.Size, expiration, ipAddress)

	return config.ServerURL + "/d/" + fileID + "/" + handler.Filename, nil
}
