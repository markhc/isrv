package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/storage"
)

// Download returns a handler that serves a stored file by its ID.
// It handles both /d/{id} and /d/{id}/{filename} patterns.
func Download(db database.Database, stor storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fileID := chi.URLParam(r, "id")
		fileName := chi.URLParam(r, "filename")

		logging.LogDebug(
			"serving file",
			logging.String("file_id", fileID),
			logging.String("file_name", fileName),
			logging.String("path", r.URL.Path))

		metadata, err := db.GetFileMetadata(fileID)
		if err != nil {
			if errors.Is(err, database.ErrFileNotFound) {
				http.NotFound(w, r)

				return
			}

			logging.LogError("failed to get file metadata", logging.Error(err))
		}

		if err := db.OnFileDownload(fileID); err != nil {
			logging.LogError("failed to update file metrics", logging.Error(err))
		}

		stor.ServeFile(w, r, fileID, fileName, metadata, true, true)
	}
}
