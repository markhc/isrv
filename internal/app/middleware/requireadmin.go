package middleware

import (
	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/app/auth"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/utils"
)

// RequireAdmin returns a middleware that enforces a valid admin session cookie.
// Requests without a valid, unexpired cookie are rejected with 401.
func RequireAdmin(cfg models.AdminConfiguration) fiber.Handler {
	secret := []byte(cfg.SessionSecret)

	return func(c fiber.Ctx) error {
		token := c.Cookies(auth.CookieName)
		if token == "" {
			return utils.RespondWithError(c, fiber.StatusUnauthorized, "authentication required")
		}

		if _, ok := auth.Validate(secret, token); !ok {
			return utils.RespondWithError(c, fiber.StatusUnauthorized, "invalid or expired session")
		}

		return c.Next()
	}
}
