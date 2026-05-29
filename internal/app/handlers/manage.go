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
	"github.com/markhc/isrv/internal/utils"
)

// Delete returns a handler that deletes a stored file by its ID.
// The file is removed from both storage and the database.
func Delete(db database.Database, st storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fileID := chi.URLParam(r, "id")

		if err := st.DeleteFile(r.Context(), fileID); err != nil {
			logging.LogError("failed to delete file from storage",
				logging.String("file_id", fileID),
				logging.Error(err),
			)
			utils.RespondWithError(w, http.StatusInternalServerError, "failed to delete file")

			return
		}

		if err := db.OnFileDelete(fileID); err != nil {
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
		fileID := chi.URLParam(r, "id")

		newExpiry, ok := parseExpireRequest(w, r)
		if !ok {
			return
		}

		record, err := db.GetFileData(fileID)
		if err != nil {
			if errors.Is(err, database.ErrFileNotFound) {
				utils.RespondWithError(w, http.StatusNotFound, "file not found")
			} else {
				logging.LogError("failed to get file data",
					logging.String("file_id", fileID),
					logging.Error(err),
				)
				utils.RespondWithError(w, http.StatusInternalServerError, "internal server error")
			}

			return
		}

		maxExpiry := utils.MaxExpirationTime(record.FileSize, config)
		if newExpiry.After(maxExpiry) {
			utils.RespondWithError(w, http.StatusUnprocessableEntity,
				"expiration exceeds the maximum allowed time of "+maxExpiry.Format(time.RFC3339),
			)

			return
		}

		if err := db.SetExpiration(fileID, newExpiry); err != nil {
			logging.LogError("failed to update expiration",
				logging.String("file_id", fileID),
				logging.Error(err),
			)
			utils.RespondWithError(w, http.StatusInternalServerError, "failed to update expiration")

			return
		}

		utils.RespondWithSuccess(w, struct {
			FileID     string `json:"fileId"`
			Expiration string `json:"expiration"`
		}{
			FileID:     fileID,
			Expiration: newExpiry.Format(time.RFC3339),
		})
	}
}
