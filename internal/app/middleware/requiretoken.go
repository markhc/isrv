package middleware

import (
	"errors"
	"fmt"
	"net/http"

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

			// Find the file associated with the token
			_, err := db.GetFileByToken(token)
			if err != nil {
				if errors.Is(err, database.ErrFileNotFound) {
					utils.RespondWithError(w, http.StatusUnauthorized, "invalid token")
				} else {
					utils.RespondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Internal Server Error: %v", err))
				}

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
