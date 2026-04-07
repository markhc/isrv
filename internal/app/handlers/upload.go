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
	"github.com/markhc/isrv/internal/utils"
)

// Upload returns a handler that accepts file uploads and stores them.
func Upload(config *models.Configuration, db database.Database, stor storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file, header, err := validateUploadRequest(r)
		if err != nil {
			utils.RespondWithError(w, http.StatusBadRequest, err.Error())

			return
		}
		defer file.Close()

		if err := validateFileSize(header, config.MaxFileSizeMB); err != nil {
			utils.RespondWithError(w, http.StatusRequestEntityTooLarge, err.Error())

			return
		}

		ipAddress := utils.GetIPAddress(r, config.TrustedProxies)
		expiration := utils.CalculateExpirationTime(r, header.Size, config)

		logging.LogInfo("file upload requested",
			logging.String("filename", header.Filename),
			logging.Int64("size", header.Size),
			logging.TimeRFC3339("expiration", expiration),
			logging.String("ip_address", ipAddress),
		)

		fileURL, err := processUpload(r.Context(), config, db, stor, file, header, expiration, ipAddress)
		if err != nil {
			logging.LogError("failed to process file upload", logging.Error(err))
			utils.RespondWithError(w, http.StatusInternalServerError, "failed to process upload")

			return
		}

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
	logging.LogInfo("processing uploaded file: " + header.Filename)

	fileID := utils.GenerateRandomString(config.RandomIDLength)

	path, err := stor.SaveFileUpload(ctx, fileID, file, header)
	if err != nil {
		logging.LogError("failed to save uploaded file", logging.Error(err))

		return "", fmt.Errorf("failed to save uploaded file: %w", err)
	}

	logging.LogInfo("file uploaded successfully", logging.String("file_id", fileID), logging.String("path", path))

	if err := db.OnFileUpload(fileID, header, expiration, ipAddress); err != nil {
		logging.LogError("failed to update file metrics", logging.Error(err))
	}

	safeFilename := url.PathEscape(header.Filename)

	return config.ServerURL + "/d/" + fileID + "/" + safeFilename, nil
}
