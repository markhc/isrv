package middleware

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/utils"
)

// RequireValidFileID returns a middleware that enforces the presence of a valid file ID in the request.
// It verifies that a file ID was provided in the request URL and that it corresponds to an existing
// file in the database.
func RequireValidFileID(db database.Database) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Find the file associated with the token
			fileID := chi.URLParam(r, "id")
			if fileID == "" {
				utils.RespondWithError(w, http.StatusBadRequest, "file ID required")

				return
			}

			_, err := db.GetFileData(fileID)
			if err != nil {
				if errors.Is(err, database.ErrFileNotFound) {
					utils.RespondWithError(w, http.StatusNotFound, "file not found")
				} else {
					utils.RespondWithError(w, http.StatusInternalServerError, "Internal Server Error")
				}

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
