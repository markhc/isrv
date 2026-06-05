package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/storage"
)

// readinessTimeout caps how long a single dependency check is allowed to run.
// Probes are called frequently by orchestrators, so they must fail fast.
const readinessTimeout = 2 * time.Second

// Healthz returns a liveness probe handler that always responds with 200 OK.
// It does not check dependencies; its only purpose is to confirm the process
// is up and the HTTP server is accepting connections.
func Healthz() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}

// Readyz returns a readiness probe handler that verifies the application's
// critical dependencies (database and storage backend) are reachable. It
// responds with 200 OK and {"status":"ok"} when all dependencies pass, or
// 503 Service Unavailable with a per-dependency status map otherwise.
func Readyz(db database.Database, stor storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		checks := map[string]string{
			"database": "ok",
			"storage":  "ok",
		}

		status := http.StatusOK

		if err := db.Ping(ctx); err != nil {
			checks["database"] = err.Error()
			status = http.StatusServiceUnavailable

			logging.WarnCtx(ctx, "readiness: database check failed", logging.Error(err))
		}

		if err := stor.HealthCheck(ctx); err != nil {
			checks["storage"] = err.Error()
			status = http.StatusServiceUnavailable

			logging.WarnCtx(ctx, "readiness: storage check failed", logging.Error(err))
		}

		overall := "ok"
		if status != http.StatusOK {
			overall = "unavailable"
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if err := json.NewEncoder(w).Encode(map[string]any{
			"status": overall,
			"checks": checks,
		}); err != nil {
			logging.ErrorCtx(ctx, "readiness: encode response failed", logging.Error(err))
		}
	}
}
