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
	"go.opentelemetry.io/otel/trace"
)

// Download returns a handler that serves a stored file by its ID.
// It is mounted on both /d/{id} and /d/{id}/{filename}.
func Download(db database.Database, stor storage.Storage) http.HandlerFunc {
	backendAttr := attribute.String(telemetry.AttrStorage, stor.Backend())

	return func(w http.ResponseWriter, r *http.Request) {
		fileID := chi.URLParam(r, "id")
		fileName := chi.URLParam(r, "filename")

		ctx, span := telemetry.Tracer().Start(r.Context(), "download.serve",
			trace.WithAttributes(
				attribute.String(telemetry.AttrFileID, fileID),
				attribute.String(telemetry.AttrFileName, fileName),
				backendAttr,
			),
		)
		defer span.End()

		fileData, err := db.GetFileData(r.Context(), fileID)
		if err != nil {
			if errors.Is(err, database.ErrFileNotFound) {
				http.NotFound(w, r)

				return
			}

			logging.ErrorCtx(r.Context(), "failed to get file data", logging.Error(err))
			http.Error(w, "internal server error", http.StatusInternalServerError)

			return
		}

		// If a file name is not present in the URL, use the original name from the database.
		if fileName == "" {
			fileName = fileData.FileName
		}

		logging.DebugCtx(ctx,
			"serving file",
			logging.String("file_id", fileID),
			logging.String("file_name", fileName),
			logging.String("path", r.URL.Path))

		if err := db.OnFileDownload(ctx, fileID); err != nil {
			// The download counter is best-effort; the file is still served.
			logging.ErrorCtx(ctx, "failed to update file metrics", logging.Error(err))
		}

		span.SetAttributes(attribute.String(telemetry.AttrResult, telemetry.ResultSuccess))
		telemetry.Downloads.Add(ctx, 1, metric.WithAttributes(
			backendAttr,
			attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
		))

		stor.ServeFile(w, r.WithContext(ctx), fileID, fileName, fileData.Metadata, true, true)
	}
}
