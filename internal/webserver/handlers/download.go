package handlers

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/storage"
)

// Download returns a handler that serves a stored file by its ID.
// It handles both /d/{fileID} and /d/{fileID}/{fileName} patterns.
func Download(db database.Database, stor storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		fileID := vars["fileID"]
		fileName := vars["fileName"]

		logging.LogDebug(
			"serving file",
			logging.String("file_id", fileID),
			logging.String("file_name", fileName),
			logging.String("path", r.URL.Path))

		if err := db.OnFileDownload(fileID); err != nil {
			logging.LogError("failed to update file metrics", logging.Error(err))
		}

		metadata, _ := db.GetFileMetadata(fileID)

		stor.ServeFile(w, r, fileID, fileName, metadata, true, true)
	}
}
