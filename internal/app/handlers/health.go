package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/storage"
)

// readinessTimeout caps how long a single dependency check is allowed to run.
const readinessTimeout = 2 * time.Second

// Healthz returns a liveness probe handler that always responds with 200 OK.
func Healthz() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Status(fiber.StatusOK).SendString(`{"status":"ok"}`)
	}
}

// Readyz returns a readiness probe handler that verifies the application's
// critical dependencies (database and storage backend) are reachable.
func Readyz(db database.Database, stor storage.Storage) fiber.Handler {
	return func(c fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), readinessTimeout)
		defer cancel()

		checks := map[string]string{
			"database": "ok",
			"storage":  "ok",
		}

		status := fiber.StatusOK

		if err := db.Ping(ctx); err != nil {
			checks["database"] = err.Error()
			status = fiber.StatusServiceUnavailable

			logging.WarnCtx(ctx, "readiness: database check failed", logging.Error(err))
		}

		if err := stor.HealthCheck(ctx); err != nil {
			checks["storage"] = err.Error()
			status = fiber.StatusServiceUnavailable

			logging.WarnCtx(ctx, "readiness: storage check failed", logging.Error(err))
		}

		overall := "ok"
		if status != fiber.StatusOK {
			overall = "unavailable"
		}

		return c.Status(status).JSON(map[string]any{
			"status": overall,
			"checks": checks,
		})
	}
}
