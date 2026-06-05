package middleware

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/utils"
)

// RequireToken returns a middleware that enforces token-based authentication
// for the {id} route. The token may be supplied as the "token" query parameter
// or in an "Authorization: Bearer <token>" header. The token must match the
// file ID in the route, otherwise the request is rejected with 401.
func RequireToken(db database.Database) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.URL.Query().Get("token")
			if token == "" {
				authHeader := r.Header.Get("Authorization")
				if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
					token = authHeader[7:]
				}
			}

			if token == "" {
				utils.RespondWithError(w, http.StatusUnauthorized, "token required")

				return
			}

			tokenFileID, err := db.GetFileByToken(r.Context(), token)
			if err != nil {
				if errors.Is(err, database.ErrFileNotFound) {
					utils.RespondWithError(w, http.StatusUnauthorized, "invalid token")
				} else {
					utils.RespondWithError(w, http.StatusInternalServerError, "Internal Server Error")
				}

				return
			}

			if tokenFileID != chi.URLParam(r, "id") {
				utils.RespondWithError(w, http.StatusUnauthorized, "invalid token")

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
