package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Download returns a handler that serves a stored file by its ID.
// It handles both /d/{id} and /d/{id}/{filename} patterns.
func Download(db database.Database, stor storage.Storage) http.HandlerFunc {
	backendAttr := attribute.String(telemetry.AttrStorage, stor.Backend())

	return func(w http.ResponseWriter, r *http.Request) {
		fileID := chi.URLParam(r, "id")
		fileName := chi.URLParam(r, "filename")

		logging.DebugCtx(r.Context(),
			"serving file",
			logging.String("file_id", fileID),
			logging.String("file_name", fileName),
			logging.String("path", r.URL.Path))

		metadata, err := db.GetFileMetadata(r.Context(), fileID)
		if err != nil {
			if errors.Is(err, database.ErrFileNotFound) {
				http.NotFound(w, r)

				return
			}

			telemetry.Downloads.Add(r.Context(), 1, metric.WithAttributes(
				backendAttr,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))

			logging.ErrorCtx(r.Context(), "failed to get file metadata", logging.Error(err))
			http.Error(w, "internal server error", http.StatusInternalServerError)

			return
		}

		if err := db.OnFileDownload(r.Context(), fileID); err != nil {
			logging.ErrorCtx(r.Context(), "failed to update file metrics", logging.Error(err))
		}

		telemetry.Downloads.Add(r.Context(), 1, metric.WithAttributes(
			backendAttr,
			attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
		))

		stor.ServeFile(w, r, fileID, fileName, metadata, true, true)
	}
}
