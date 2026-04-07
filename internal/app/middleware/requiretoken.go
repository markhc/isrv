package middleware

import (
	"net/http"

	"github.com/markhc/isrv/internal/models"
)

// RequireToken returns a middleware that enforces token-based authentication.
// Not yet implemented - currently a passthrough stub.
func RequireToken(_ *models.Configuration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return next
	}
}
