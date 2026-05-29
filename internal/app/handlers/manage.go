package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
	"github.com/markhc/isrv/internal/utils"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Delete returns a handler that deletes a stored file by its ID.
// The file is removed from both storage and the database.
func Delete(db database.Database, st storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fileID := chi.URLParam(r, "id")

		ctx, storSpan := telemetry.Tracer().Start(r.Context(), "storage.delete_file",
			trace.WithAttributes(attribute.String("file.id", fileID)),
		)
		err := st.DeleteFile(ctx, fileID)
		storSpan.End()

		if err != nil {
			storSpan.RecordError(err)
			storSpan.SetStatus(codes.Error, "failed to delete file from storage")
			logging.LogError("failed to delete file from storage",
				logging.String("file_id", fileID),
				logging.Error(err),
			)
			utils.RespondWithError(w, http.StatusInternalServerError, "failed to delete file")

			return
		}

		ctx, dbSpan := telemetry.Tracer().Start(r.Context(), "db.delete_record",
			trace.WithAttributes(attribute.String("file.id", fileID)),
		)
		err = db.OnFileDelete(ctx, fileID)
		dbSpan.End()

		if err != nil {
			dbSpan.RecordError(err)
			dbSpan.SetStatus(codes.Error, "failed to remove file record from database")
			logging.LogError("failed to remove file record from database",
				logging.String("file_id", fileID),
				logging.Error(err),
			)
			utils.RespondWithError(w, http.StatusInternalServerError, "failed to delete file record")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// expireRequest is the JSON body accepted by the Expire handler.
type expireRequest struct {
	// Expires is a Unix timestamp in milliseconds, or a number of hours from now
	// (values below 1,000,000 are treated as hours).
	Expires string `json:"expires"`
}

// parseExpireRequest decodes and validates the Expire request body.
// Returns the parsed expiry time and true on success, or writes an error response and returns false.
func parseExpireRequest(w http.ResponseWriter, r *http.Request) (time.Time, bool) {
	var body expireRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, "invalid request body")

		return time.Time{}, false
	}

	if body.Expires == "" {
		utils.RespondWithError(w, http.StatusBadRequest, "'expires' field is required")

		return time.Time{}, false
	}

	newExpiry, err := utils.ParseExpiresForm(body.Expires)
	if err != nil {
		utils.RespondWithError(w, http.StatusBadRequest, fmt.Sprintf("invalid expires value: %v", err))

		return time.Time{}, false
	}

	return newExpiry, true
}

// Expire returns a handler that updates the expiration time of a stored file.
// The new expiration must not exceed the maximum allowed expiration derived
// from the file's size and the configured min/max age settings.
func Expire(config *models.Configuration, db database.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		applyExpire(w, r, config, db)
	}
}

//nolint:funlen
func applyExpire(w http.ResponseWriter, r *http.Request, config *models.Configuration, db database.Database) {
	fileID := chi.URLParam(r, "id")

	newExpiry, ok := parseExpireRequest(w, r)
	if !ok {
		return
	}

	ctx, getSpan := telemetry.Tracer().Start(r.Context(), "db.get_file_data",
		trace.WithAttributes(attribute.String("file.id", fileID)),
	)
	record, err := db.GetFileData(ctx, fileID)
	if err != nil {
		if errors.Is(err, database.ErrFileNotFound) {
			utils.RespondWithError(w, http.StatusNotFound, "file not found")
		} else {
			getSpan.RecordError(err)
			getSpan.SetStatus(codes.Error, "failed to get file data")
			logging.LogError("failed to get file data",
				logging.String("file_id", fileID),
				logging.Error(err),
			)
			utils.RespondWithError(w, http.StatusInternalServerError, "internal server error")
		}

		getSpan.End()

		return
	}

	getSpan.End()

	maxExpiry := utils.MaxExpirationTime(record.FileSize, config)
	if newExpiry.After(maxExpiry) {
		utils.RespondWithError(w, http.StatusUnprocessableEntity,
			"expiration exceeds the maximum allowed time of "+maxExpiry.Format(time.RFC3339),
		)

		return
	}

	ctx, setSpan := telemetry.Tracer().Start(r.Context(), "db.set_expiration",
		trace.WithAttributes(
			attribute.String("file.id", fileID),
			attribute.String("file.new_expiry", newExpiry.Format(time.RFC3339)),
		),
	)
	err = db.SetExpiration(ctx, fileID, newExpiry)
	if err != nil {
		setSpan.RecordError(err)
		setSpan.SetStatus(codes.Error, "failed to update expiration")
		logging.LogError("failed to update expiration",
			logging.String("file_id", fileID),
			logging.Error(err),
		)
		utils.RespondWithError(w, http.StatusInternalServerError, "failed to update expiration")

		setSpan.End()

		return
	}

	setSpan.End()

	utils.RespondWithSuccess(w, struct {
		FileID     string `json:"fileId"`
		Expiration string `json:"expiration"`
	}{
		FileID:     fileID,
		Expiration: newExpiry.Format(time.RFC3339),
	})
}
