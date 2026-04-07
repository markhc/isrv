package handlers

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/headers"
	"github.com/markhc/isrv/internal/logging"
)

// Static returns a handler that serves embedded static files.
// It blocks path traversal attempts.
func Static(staticFilesDir fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logging.LogDebug("staticFilesHandler", logging.String("path", r.URL.Path))

		file := chi.URLParam(r, "file")

		if strings.Contains(file, "..") {
			http.Error(w, "Invalid file path", http.StatusBadRequest)

			return
		}

		headers.AddCacheHeader(w)

		http.StripPrefix("/static/", http.FileServer(http.FS(staticFilesDir))).ServeHTTP(w, r)
	}
}

// Favicon returns a handler that serves the pre-fetched favicon bytes.
func Favicon(data []byte, format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers.AddCacheHeader(w)
		headers.SetContentType(w, "image/"+format)
		if _, err := w.Write(data); err != nil {
			logging.LogError("failed to write favicon response", logging.Error(err))
		}
	}
}
