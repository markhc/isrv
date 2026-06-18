package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/utils"
)

// RequireValidFileID returns a middleware that rejects requests whose :id
// path parameter is missing or does not correspond to a known file. It
// responds with 400 when the parameter is absent, 404 when no matching file
// exists, and 500 on any other database error.
func RequireValidFileID(db database.Database) fiber.Handler {
	return func(c fiber.Ctx) error {
		fileID := c.Params("id")
		if fileID == "" {
			return utils.RespondWithError(c, fiber.StatusBadRequest, "file ID required")
		}

		_, err := db.GetFileData(c.Context(), fileID)
		if err != nil {
			if errors.Is(err, database.ErrFileNotFound) {
				return utils.RespondWithError(c, fiber.StatusNotFound, "file not found")
			}
			return utils.RespondWithError(c, fiber.StatusInternalServerError, "Internal Server Error")
		}

		return c.Next()
	}
}
