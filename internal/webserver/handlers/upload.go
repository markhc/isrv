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
		file, header, err := validateUploadRequest(w, r, config)
		if err != nil {
			if err.Error() == "file too large" {
				logging.LogInfo(
					"file upload rejected: file too large",
					logging.Int("max_size_bytes", config.MaxFileSizeMB*1024*1024))
				utils.RespondWithError(w, http.StatusRequestEntityTooLarge, "file too large")
			} else {
				logging.LogInfo("file upload rejected: invalid request", logging.Error(err))
				utils.RespondWithError(w, http.StatusBadRequest, err.Error())
			}

			return
		}
		defer file.Close()

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

func validateUploadRequest(
	w http.ResponseWriter,
	r *http.Request,
	config *models.Configuration,
) (multipart.File, *multipart.FileHeader, error) {
	// validate file size first
	maxSizeBytes := int64(config.MaxFileSizeMB * 1024 * 1024)
	r.Body = http.MaxBytesReader(w, r.Body, maxSizeBytes)

	file, header, err := r.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, nil, errors.New("file too large")
		}

		return nil, nil, errors.New("missing multipart form 'file' field")
	}

	return file, header, nil
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
	logging.LogInfo("processing upload", logging.String("filename", header.Filename))

	fileID := utils.GenerateRandomString(config.RandomIDLength)

	logging.LogDebug("generated file ID", logging.String("file_id", fileID))

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
