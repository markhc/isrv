package middleware

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/utils"
)

// RequireToken returns a middleware that enforces token-based authentication
// for the :id route. The token may be supplied as the "token" query parameter
// or in an "Authorization: Bearer <token>" header. The token must match the
// file ID in the route, otherwise the request is rejected with 401.
func RequireToken(db database.Database) fiber.Handler {
	return func(c fiber.Ctx) error {
		token := c.Query("token")
		if token == "" {
			authHeader := c.Get("Authorization")
			if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				token = authHeader[7:]
			}
		}

		if token == "" {
			return utils.RespondWithError(c, fiber.StatusUnauthorized, "token required")
		}

		tokenFileID, err := db.GetFileByToken(c.Context(), token)
		if err != nil {
			if errors.Is(err, database.ErrFileNotFound) {
				return utils.RespondWithError(c, fiber.StatusUnauthorized, "invalid token")
			}
			return utils.RespondWithError(c, fiber.StatusInternalServerError, "Internal Server Error")
		}

		if tokenFileID != c.Params("id") {
			return utils.RespondWithError(c, fiber.StatusUnauthorized, "invalid token")
		}

		return c.Next()
	}
}
