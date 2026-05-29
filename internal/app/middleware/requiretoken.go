package middleware

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/utils"
)

// RequireToken returns a middleware that enforces token-based authentication.
// It verifies that a token was provided in the request, either as a query parameter
// or in the Authorization header.
func RequireToken(db database.Database) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check for token in query parameters
			token := r.URL.Query().Get("token")
			if token == "" {
				// Check for token in Authorization header
				authHeader := r.Header.Get("Authorization")
				if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
					token = authHeader[7:]
				}
			}

			if token == "" {
				utils.RespondWithError(w, http.StatusUnauthorized, "token required")

				return
			}

			// Verify the token belongs to the file being acted on.
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
