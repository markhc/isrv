package handlers

import (
	"context"
	"fmt"
	"mime/multipart"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
	"github.com/markhc/isrv/internal/utils"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type uploadResponse struct {
	Status     string `json:"status"`
	Filename   string `json:"filename"`
	FileToken  string `json:"fileToken"`
	ShortURL   string `json:"shortUrl"`
	Expiration string `json:"expiration"`
}

// Upload returns a handler that accepts a multipart file upload and persists
// it to storage along with a database record.
//
//nolint:funlen
func Upload(config *models.Configuration, db database.Database, stor storage.Storage) fiber.Handler {
	backendAttr := attribute.String(telemetry.AttrStorage, stor.Backend())

	return func(c fiber.Ctx) error {
		header, err := c.FormFile("file")
		if err != nil {
			telemetry.Uploads.Add(c.Context(), 1, metric.WithAttributes(
				backendAttr,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			return utils.RespondWithError(c, fiber.StatusBadRequest, "multipart form 'file' field is missing")
		}

		if err := validateFileSize(header, config.MaxFileSizeMB); err != nil {
			telemetry.Uploads.Add(c.Context(), 1, metric.WithAttributes(
				backendAttr,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			return utils.RespondWithError(c, fiber.StatusRequestEntityTooLarge, err.Error())
		}

		file, err := header.Open()
		if err != nil {
			telemetry.Uploads.Add(c.Context(), 1, metric.WithAttributes(
				backendAttr,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			return utils.RespondWithError(c, fiber.StatusBadRequest, "failed to open uploaded file")
		}
		defer file.Close()

		ipAddress := utils.GetIPAddress(c, config.TrustedProxies)
		expiration := utils.CalculateExpirationTime(c, header.Size, config)

		logging.InfoCtx(c.Context(), "file upload requested",
			logging.String("filename", header.Filename),
			logging.Int64("size", header.Size),
			logging.TimeRFC3339("expiration", expiration),
			logging.MaybeIP("ip_address", ipAddress),
		)

		uploadResp, err := processUpload(c.Context(), config, db, stor, file, header, expiration, ipAddress)
		if err != nil {
			telemetry.Uploads.Add(c.Context(), 1, metric.WithAttributes(
				backendAttr,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			logging.ErrorCtx(c.Context(), "failed to process file upload", logging.Error(err))
			return utils.RespondWithError(c, fiber.StatusInternalServerError, "failed to process upload")
		}

		telemetry.Uploads.Add(c.Context(), 1, metric.WithAttributes(
			backendAttr,
			attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
		))
		telemetry.UploadSize.Record(c.Context(), header.Size, metric.WithAttributes(backendAttr))

		return utils.RespondWithSuccess(c, uploadResp)
	}
}

func validateFileSize(header *multipart.FileHeader, maxFileSizeMB int) error {
	maxSizeBytes := int64(maxFileSizeMB * 1024 * 1024)
	if header.Size > maxSizeBytes {
		return fmt.Errorf("file size exceeds the maximum allowed limit of %d MB", maxFileSizeMB)
	}
	return nil
}

//nolint:funlen
func processUpload(
	ctx context.Context,
	config *models.Configuration,
	db database.Database,
	stor storage.Storage,
	file multipart.File,
	header *multipart.FileHeader,
	expiration time.Time,
	ipAddress string,
) (uploadResponse, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "upload.process_file",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileName, header.Filename),
			attribute.String(telemetry.AttrRequestIP, ipAddress),
			attribute.Int64(telemetry.AttrFileSize, header.Size),
		),
	)
	defer span.End()

	logging.InfoCtx(ctx, "processing uploaded file", logging.String("filename", header.Filename))

	fileID := utils.GenerateRandomString(config.RandomIDLength)
	span.SetAttributes(attribute.String(telemetry.AttrFileID, fileID))

	logging.DebugCtx(ctx, "generated file ID", logging.String("file_id", fileID))

	if !config.KeepOriginalFilename {
		header.Filename = fileID + utils.GetFileExtension(header.Filename)
	}

	token, err := utils.GenerateFileToken()
	if err != nil {
		logging.ErrorCtx(ctx, "failed to generate file token", logging.Error(err))
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to generate file token")
		return uploadResponse{}, fmt.Errorf("failed to generate file token: %w", err)
	}

	path, err := stor.SaveFileUpload(ctx, fileID, file, header)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to save uploaded file")
		logging.ErrorCtx(ctx, "failed to save uploaded file", logging.Error(err))
		return uploadResponse{}, fmt.Errorf("failed to save uploaded file: %w", err)
	}

	if err := db.OnFileUpload(ctx, fileID, header, token, expiration, ipAddress); err != nil {
		if rollbackErr := stor.DeleteFile(ctx, fileID); rollbackErr != nil {
			logging.ErrorCtx(ctx, "failed to roll back stored file after db error",
				logging.String("file_id", fileID),
				logging.Error(rollbackErr),
			)
		}

		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to record file upload in database")
		logging.ErrorCtx(ctx, "failed to record file upload in database",
			logging.String("file_id", fileID),
			logging.Error(err),
		)
		return uploadResponse{}, fmt.Errorf("failed to record file upload: %w", err)
	}

	logging.InfoCtx(ctx, "file uploaded successfully",
		logging.String("file_id", fileID),
		logging.String("path", path),
	)

	safeFilename := url.PathEscape(header.Filename)

	return uploadResponse{
		Status:     "success",
		Filename:   config.ServerURL + "/d/" + fileID + "/" + safeFilename,
		ShortURL:   config.ServerURL + "/d/" + fileID,
		Expiration: expiration.Format(time.RFC3339),
		FileToken:  token,
	}, nil
}
