package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
	"github.com/markhc/isrv/internal/utils"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// userDeleteSource labels FilesDeleted observations originating from a user-initiated delete.
var userDeleteSource = attribute.String(telemetry.AttrSource, "user")

// Delete returns a handler that removes a stored file by its ID from both
// storage and the database.
func Delete(db database.Database, st storage.Storage) fiber.Handler {
	return func(c fiber.Ctx) error {
		fileID := c.Params("id")

		err := st.DeleteFile(c.Context(), fileID)
		if err != nil {
			logging.ErrorCtx(c.Context(), "failed to delete file from storage",
				logging.String("file_id", fileID),
				logging.Error(err),
			)
			telemetry.FilesDeleted.Add(c.Context(), 1, metric.WithAttributes(
				userDeleteSource,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			return utils.RespondWithError(c, fiber.StatusInternalServerError, "failed to delete file")
		}

		err = db.OnFileDelete(c.Context(), fileID)
		if err != nil {
			logging.ErrorCtx(c.Context(), "failed to remove file record from database",
				logging.String("file_id", fileID),
				logging.Error(err),
			)
			telemetry.FilesDeleted.Add(c.Context(), 1, metric.WithAttributes(
				userDeleteSource,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))
			return utils.RespondWithError(c, fiber.StatusInternalServerError, "failed to delete file record")
		}

		telemetry.FilesDeleted.Add(c.Context(), 1, metric.WithAttributes(
			userDeleteSource,
			attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
		))

		return c.SendStatus(fiber.StatusNoContent)
	}
}

// expireRequest is the JSON body accepted by the Expire handler.
type expireRequest struct {
	// Expires is a Unix timestamp in milliseconds, or a number of hours from now.
	Expires string `json:"expires"`
}

// parseExpireRequest decodes and validates the Expire request body.
func parseExpireRequest(c fiber.Ctx) (time.Time, bool, error) {
	var body expireRequest
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return time.Time{}, false, utils.RespondWithError(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Expires == "" {
		return time.Time{}, false, utils.RespondWithError(c, fiber.StatusBadRequest, "'expires' field is required")
	}

	newExpiry, err := utils.ParseExpiresForm(body.Expires)
	if err != nil {
		msg := fmt.Sprintf("invalid expires value: %v", err)
		return time.Time{}, false, utils.RespondWithError(c, fiber.StatusBadRequest, msg)
	}

	return newExpiry, true, nil
}

// Expire returns a handler that updates the expiration time of a stored file.
func Expire(config *models.Configuration, db database.Database) fiber.Handler {
	return func(c fiber.Ctx) error {
		return applyExpire(c, config, db)
	}
}

func applyExpire(c fiber.Ctx, config *models.Configuration, db database.Database) error {
	fileID := c.Params("id")

	newExpiry, ok, respErr := parseExpireRequest(c)
	if !ok {
		return respErr
	}

	record, err := db.GetFile(c.Context(), fileID)
	if err != nil {
		if errors.Is(err, database.ErrFileNotFound) {
			return utils.RespondWithError(c, fiber.StatusNotFound, "file not found")
		}
		logging.ErrorCtx(c.Context(), "failed to get file data",
			logging.String("file_id", fileID),
			logging.Error(err),
		)
		return utils.RespondWithError(c, fiber.StatusInternalServerError, "internal server error")
	}

	maxExpiry := utils.MaxExpirationTime(record.Size, config)
	if newExpiry.After(maxExpiry) {
		return utils.RespondWithError(c, fiber.StatusUnprocessableEntity,
			"expiration exceeds the maximum allowed time of "+maxExpiry.Format(time.RFC3339),
		)
	}

	err = db.SetExpiration(c.Context(), fileID, newExpiry)
	if err != nil {
		logging.ErrorCtx(c.Context(), "failed to update expiration",
			logging.String("file_id", fileID),
			logging.Error(err),
		)
		return utils.RespondWithError(c, fiber.StatusInternalServerError, "failed to update expiration")
	}

	return utils.RespondWithSuccess(c, struct {
		FileID     string `json:"fileId"`
		Expiration string `json:"expiration"`
	}{
		FileID:     fileID,
		Expiration: newExpiry.Format(time.RFC3339),
	})
}
