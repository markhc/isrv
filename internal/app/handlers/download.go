package handlers

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/headers"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
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

		// Prefer offloading delivery to the backend (S3 pre-signed redirect)
		// when it supports it.
		url, ok, err := stor.PresignedURL(ctx, file)
		if err != nil {
			logging.ErrorCtx(ctx, "failed to generate presigned url", logging.Error(err))
			return c.Status(fiber.StatusInternalServerError).SendString("failed to generate file url")
		}

		if ok {
			recordDownloadSuccess(ctx, span, backendAttr)
			return c.Redirect().Status(fiber.StatusFound).To(url)
		}

		return serveStream(c, stor, file, span, backendAttr)
	}
}

// serveStream opens the object from storage and streams it to the client,
// owning all HTTP concerns: range handling, headers, and status codes.
func serveStream(
	c fiber.Ctx,
	stor storage.Storage,
	file *models.File,
	span trace.Span,
	backendAttr attribute.KeyValue,
) error {
	ctx := c.Context()
	brange := parseRangeHeader(c.Get(fiber.HeaderRange))

	obj, err := stor.Open(ctx, file.ID, brange)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrObjectNotFound):
			return c.Status(fiber.StatusNotFound).SendString("not found")
		case errors.Is(err, storage.ErrInvalidRange):
			return c.Status(fiber.StatusRequestedRangeNotSatisfiable).SendString("invalid range")
		default:
			logging.ErrorCtx(ctx, "failed to open file", logging.Error(err))
			return c.Status(fiber.StatusInternalServerError).SendString("failed to fetch file")
		}
	}

	contentType := file.ContentType
	if contentType == "" {
		contentType = obj.ContentType
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	headers.SetHeaders(c, file.Name, contentType, true, true)
	c.Set(fiber.HeaderAcceptRanges, "bytes")

	if obj.Partial {
		c.Set(fiber.HeaderContentRange, obj.ContentRange)
		c.Status(fiber.StatusPartialContent)
	}

	recordDownloadSuccess(ctx, span, backendAttr)

	// SendStream closes obj.Body once the response has been written.
	return c.SendStream(obj.Body, int(obj.Length))
}

// recordDownloadSuccess marks the span and increments the downloads counter
// for a successful delivery.
func recordDownloadSuccess(ctx context.Context, span trace.Span, backendAttr attribute.KeyValue) {
	span.SetAttributes(attribute.String(telemetry.AttrResult, telemetry.ResultSuccess))
	telemetry.Downloads.Add(ctx, 1, metric.WithAttributes(
		backendAttr,
		attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
	))
}

// parseRangeHeader parses a single HTTP byte-range request header value. It
// returns nil when the header is absent, malformed, or specifies multiple
// ranges, in which case the caller serves the full object.
func parseRangeHeader(header string) *storage.ByteRange {
	const prefix = "bytes="

	if !strings.HasPrefix(header, prefix) {
		return nil
	}

	spec := strings.TrimPrefix(header, prefix)
	if strings.Contains(spec, ",") {
		// Multiple ranges are not supported; serve the whole object.
		return nil
	}

	startStr, endStr, ok := strings.Cut(spec, "-")
	if !ok {
		return nil
	}

	startStr = strings.TrimSpace(startStr)
	endStr = strings.TrimSpace(endStr)

	// Suffix range: "-N" (last N bytes).
	if startStr == "" {
		suffix, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || suffix <= 0 {
			return nil
		}

		return &storage.ByteRange{Suffix: suffix}
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return nil
	}

	// Open-ended range: "N-".
	if endStr == "" {
		return &storage.ByteRange{Start: start, End: -1}
	}

	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return nil
	}

	return &storage.ByteRange{Start: start, End: end}
}
