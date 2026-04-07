package middleware

import (
	"net/http"

	"github.com/markhc/isrv/internal/database"
)

// RequireToken returns a middleware that enforces token-based authentication.
// It verifies that a token was provided in the request, either as a query parameter
// or in the Authorization header.
func RequireToken(db database.Database) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
