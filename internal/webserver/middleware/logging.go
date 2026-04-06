package middleware

import (
	"net/http"

	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/utils"
)

func WithRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logging.LogInfo(
			"incoming request",
			logging.String("method", r.Method),
			logging.String("path", r.URL.Path),
			logging.Int64("body_size", r.ContentLength),
			logging.String("ip_address", utils.GetIPAddress(r)))
		next.ServeHTTP(w, r)
	})
}
