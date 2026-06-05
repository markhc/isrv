package handlers

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

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

// Upload returns a handler that accepts file uploads and stores them.
//
//nolint:funlen // Handler body is mostly inline error-path branches that emit metrics; splitting harms readability.
func Upload(config *models.Configuration, db database.Database, stor storage.Storage) http.HandlerFunc {
	backendAttr := attribute.String(telemetry.AttrStorage, stor.Backend())

	return func(w http.ResponseWriter, r *http.Request) {
		file, header, err := validateUploadRequest(r)
		if err != nil {
			telemetry.Uploads.Add(r.Context(), 1, metric.WithAttributes(
				backendAttr,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			utils.RespondWithError(w, http.StatusBadRequest, err.Error())

			return
		}
		defer file.Close()

		if err := validateFileSize(header, config.MaxFileSizeMB); err != nil {
			telemetry.Uploads.Add(r.Context(), 1, metric.WithAttributes(
				backendAttr,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			utils.RespondWithError(w, http.StatusRequestEntityTooLarge, err.Error())

			return
		}

		ipAddress := utils.GetIPAddress(r, config.TrustedProxies)
		expiration := utils.CalculateExpirationTime(r, header.Size, config)

		logging.InfoCtx(r.Context(), "file upload requested",
			logging.String("filename", header.Filename),
			logging.Int64("size", header.Size),
			logging.TimeRFC3339("expiration", expiration),
			logging.MaybeIP("ip_address", ipAddress),
		)

		fileURL, err := processUpload(r.Context(), config, db, stor, file, header, expiration, ipAddress)
		if err != nil {
			telemetry.Uploads.Add(r.Context(), 1, metric.WithAttributes(
				backendAttr,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			logging.ErrorCtx(r.Context(), "failed to process file upload", logging.Error(err))
			utils.RespondWithError(w, http.StatusInternalServerError, "failed to process upload")

			return
		}

		telemetry.Uploads.Add(r.Context(), 1, metric.WithAttributes(
			backendAttr,
			attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
		))
		telemetry.UploadSize.Record(r.Context(), header.Size, metric.WithAttributes(backendAttr))

		utils.RespondWithSuccess(w, struct {
			Status     string `json:"status"`
			Filename   string `json:"filename"`
			Expiration string `json:"expiration"`
		}{
			Status:     "success",
			Filename:   fileURL,
			Expiration: expiration.Format(time.RFC3339),
		})
	}
}

func validateUploadRequest(r *http.Request) (multipart.File, *multipart.FileHeader, error) {
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, nil, errors.New("multipart form 'file' field is missing")
	}

	return file, header, nil
}

func validateFileSize(header *multipart.FileHeader, maxFileSizeMB int) error {
	maxSizeBytes := int64(maxFileSizeMB * 1024 * 1024)
	if header.Size > maxSizeBytes {
		return fmt.Errorf("file size exceeds the maximum allowed limit of %d MB", maxFileSizeMB)
	}

	return nil
}

func processUpload(
	ctx context.Context,
	config *models.Configuration,
	db database.Database,
	stor storage.Storage,
	file multipart.File,
	header *multipart.FileHeader,
	expiration time.Time,
	ipAddress string,
) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "upload.process_file",
		trace.WithAttributes(
			attribute.String("file.name", header.Filename),
			attribute.String("request.ip_address", ipAddress),
			attribute.Int64("file.size_bytes", header.Size),
		),
	)

	defer span.End()

	logging.InfoCtx(ctx, "processing uploaded file", logging.String("filename", header.Filename))

	fileID := utils.GenerateRandomString(config.RandomIDLength)

	logging.DebugCtx(ctx, "generated file ID", logging.String("file_id", fileID))

	token, err := utils.GenerateFileToken()
	if err != nil {
		logging.ErrorCtx(ctx, "failed to generate file token", logging.Error(err))

		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to generate file token")

		return "", fmt.Errorf("failed to generate file token: %w", err)
	}

	path, err := saveToStorage(ctx, stor, fileID, file, header)
	if err != nil {
		return "", err
	}

	logging.InfoCtx(ctx, "file uploaded successfully",
		logging.String("file_id", fileID),
		logging.String("path", path),
	)

	if err := recordInDatabase(ctx, db, fileID, header, token, expiration, ipAddress); err != nil {
		if rollbackErr := stor.DeleteFile(ctx, fileID); rollbackErr != nil {
			logging.ErrorCtx(ctx, "failed to roll back stored file after db error",
				logging.String("file_id", fileID),
				logging.Error(rollbackErr),
			)
		}

		return "", err
	}

	safeFilename := url.PathEscape(header.Filename)

	return config.ServerURL + "/d/" + fileID + "/" + safeFilename, nil
}

func saveToStorage(
	ctx context.Context,
	stor storage.Storage,
	fileID string,
	file multipart.File,
	header *multipart.FileHeader,
) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "storage.save_file",
		trace.WithAttributes(
			attribute.String("file.id", fileID),
			attribute.String("file.name", header.Filename),
			attribute.Int64("file.size_bytes", header.Size),
		),
	)
	defer span.End()

	path, err := stor.SaveFileUpload(ctx, fileID, file, header)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to save uploaded file")
		logging.ErrorCtx(ctx, "failed to save uploaded file", logging.Error(err))

		return "", fmt.Errorf("failed to save uploaded file: %w", err)
	}

	return path, nil
}

func recordInDatabase(
	ctx context.Context,
	db database.Database,
	fileID string,
	header *multipart.FileHeader,
	token string,
	expiration time.Time,
	ipAddress string,
) error {
	ctx, span := telemetry.Tracer().Start(ctx, "db.record_upload",
		trace.WithAttributes(
			attribute.String("file.id", fileID),
			attribute.String("file.name", header.Filename),
			attribute.Int64("file.size_bytes", header.Size),
		),
	)
	defer span.End()

	if err := db.OnFileUpload(ctx, fileID, header, token, expiration, ipAddress); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to record file upload in database")
		logging.ErrorCtx(ctx, "failed to record file upload in database",
			logging.String("file_id", fileID),
			logging.Error(err),
		)

		return fmt.Errorf("failed to record file upload: %w", err)
	}

	return nil
}
