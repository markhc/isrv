package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Download returns a handler that serves a stored file by its ID.
// It is mounted on both /d/:id and /d/:id/:filename.
func Download(db database.Database, stor storage.Storage) fiber.Handler {
	backendAttr := attribute.String(telemetry.AttrStorage, stor.Backend())

	return func(c fiber.Ctx) error {
		fileID := c.Params("id")
		fileName := c.Params("filename")

		ctx, span := telemetry.Tracer().Start(c.Context(), "download.serve",
			trace.WithAttributes(
				attribute.String(telemetry.AttrFileID, fileID),
				attribute.String(telemetry.AttrFileName, fileName),
				backendAttr,
			),
		)
		defer span.End()
		c.SetContext(ctx)

		file, err := db.GetFile(ctx, fileID)
		if err != nil {
			if errors.Is(err, database.ErrFileNotFound) {
				return c.Status(fiber.StatusNotFound).SendString("not found")
			}

			logging.ErrorCtx(ctx, "failed to get file data", logging.Error(err))
			return c.Status(fiber.StatusInternalServerError).SendString("internal server error")
		}

		if fileName != "" {
			file.Name = fileName
		}

		logging.DebugCtx(ctx,
			"serving file",
			logging.String("id", fileID),
			logging.String("filename", file.Name),
			logging.String("path", c.Path()))

		if err := db.OnFileDownload(ctx, fileID); err != nil {
			// best-effort
			logging.ErrorCtx(ctx, "failed to update file metrics", logging.Error(err))
		}

		span.SetAttributes(attribute.String(telemetry.AttrResult, telemetry.ResultSuccess))
		telemetry.Downloads.Add(ctx, 1, metric.WithAttributes(
			backendAttr,
			attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
		))

		return stor.ServeFile(c, file)
	}
}
