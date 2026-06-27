package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/app/auth"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
	"github.com/markhc/isrv/internal/utils"
	"go.opentelemetry.io/otel/attribute"
)

// adminDeleteSource labels FilesDeleted observations originating from an admin-initiated delete.
var adminDeleteSource = attribute.String(telemetry.AttrSource, "admin")

const (
	defaultAdminPageSize = 50
	maxAdminPageSize     = 200
)

// loginRequest is the JSON body accepted by the admin login handler.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// sessionResponse describes the current admin authentication state.
type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
}

// AdminLogin returns a handler that validates admin credentials and, on
// success, sets a signed session cookie.
func AdminLogin(cfg models.AdminConfiguration) fiber.Handler {
	secret := []byte(cfg.SessionSecret)

	return func(c fiber.Ctx) error {
		var body loginRequest
		if err := json.Unmarshal(c.Body(), &body); err != nil {
			return utils.RespondWithError(c, fiber.StatusBadRequest, "invalid request body")
		}

		userMatch := subtle.ConstantTimeCompare([]byte(body.Username), []byte(cfg.Username)) == 1
		passMatch := subtle.ConstantTimeCompare([]byte(body.Password), []byte(cfg.Password)) == 1
		if !userMatch || !passMatch {
			return utils.RespondWithError(c, fiber.StatusUnauthorized, "invalid credentials")
		}

		token := auth.IssueToken(secret, cfg.Username, cfg.SessionTTL)
		c.Cookie(&fiber.Cookie{
			Name:     auth.CookieName,
			Value:    token,
			Path:     "/admin",
			Expires:  time.Now().Add(cfg.SessionTTL),
			HTTPOnly: true,
			Secure:   c.Secure(),
			SameSite: fiber.CookieSameSiteStrictMode,
		})

		return utils.RespondWithSuccess(c, sessionResponse{Authenticated: true, Username: cfg.Username})
	}
}

// AdminLogout returns a handler that clears the admin session cookie.
func AdminLogout() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Cookie(&fiber.Cookie{
			Name:     auth.CookieName,
			Value:    "",
			Path:     "/admin",
			Expires:  time.Now().Add(-time.Hour),
			HTTPOnly: true,
			Secure:   c.Secure(),
			SameSite: fiber.CookieSameSiteStrictMode,
		})

		return c.SendStatus(fiber.StatusNoContent)
	}
}

// AdminSession returns a handler that reports whether the request carries a
// valid admin session. It is unauthenticated so the frontend can bootstrap.
func AdminSession(cfg models.AdminConfiguration) fiber.Handler {
	secret := []byte(cfg.SessionSecret)

	return func(c fiber.Ctx) error {
		username, ok := auth.Validate(secret, c.Cookies(auth.CookieName))
		if !ok {
			return utils.RespondWithSuccess(c, sessionResponse{Authenticated: false})
		}

		return utils.RespondWithSuccess(c, sessionResponse{Authenticated: true, Username: username})
	}
}

// listFilesResponse is the paginated payload returned by AdminListFiles.
type listFilesResponse struct {
	Items  []models.File `json:"items"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

// AdminListFiles returns a handler that lists, searches and paginates uploaded
// files for the admin panel.
func AdminListFiles(db database.Database) fiber.Handler {
	return func(c fiber.Ctx) error {
		filter := models.FileListFilter{
			Search:  c.Query("search"),
			IP:      c.Query("ip"),
			SortBy:  c.Query("sortBy"),
			SortDir: c.Query("sortDir"),
			Limit:   clampLimit(c.Query("limit")),
			Offset:  parseOffset(c.Query("offset")),
		}

		files, total, err := db.ListFiles(c.Context(), filter)
		if err != nil {
			logging.ErrorCtx(c.Context(), "failed to list files", logging.Error(err))
			return utils.RespondWithError(c, fiber.StatusInternalServerError, "failed to list files")
		}

		return utils.RespondWithSuccess(c, listFilesResponse{
			Items:  files,
			Total:  total,
			Limit:  filter.Limit,
			Offset: filter.Offset,
		})
	}
}

// AdminDeleteFile returns a handler that deletes a stored file by ID, bypassing
// the per-file upload token. It is intended to run behind RequireAdmin.
func AdminDeleteFile(db database.Database, st storage.Storage) fiber.Handler {
	return func(c fiber.Ctx) error {
		fileID := c.Params("id")

		if _, err := db.GetFile(c.Context(), fileID); err != nil {
			if errors.Is(err, database.ErrFileNotFound) {
				return utils.RespondWithError(c, fiber.StatusNotFound, "file not found")
			}
			logging.ErrorCtx(c.Context(), "failed to look up file", logging.Error(err))
			return utils.RespondWithError(c, fiber.StatusInternalServerError, "internal server error")
		}

		if err := deleteFile(c.Context(), db, st, fileID, adminDeleteSource); err != nil {
			return utils.RespondWithError(c, fiber.StatusInternalServerError, "failed to delete file")
		}

		return c.SendStatus(fiber.StatusNoContent)
	}
}

// clampLimit parses a page-size query value, applying a default and an upper bound.
func clampLimit(raw string) int {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return defaultAdminPageSize
	}
	if limit > maxAdminPageSize {
		return maxAdminPageSize
	}

	return limit
}

// parseOffset parses a non-negative offset query value, defaulting to 0.
func parseOffset(raw string) int {
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0
	}

	return offset
}
