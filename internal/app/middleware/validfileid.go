package middleware

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/utils"
)

// RequireValidFileID returns a middleware that rejects requests whose {id}
// path parameter is missing or does not correspond to a known file. It
// responds with 400 when the parameter is absent, 404 when no matching file
// exists, and 500 on any other database error.
func RequireValidFileID(db database.Database) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fileID := chi.URLParam(r, "id")
			if fileID == "" {
				utils.RespondWithError(w, http.StatusBadRequest, "file ID required")

				return
			}

			_, err := db.GetFileData(r.Context(), fileID)
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
