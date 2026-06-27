package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/app/auth"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAdminApp(cfg models.AdminConfiguration) *fiber.App {
	app := fiber.New()
	app.Get("/protected", RequireAdmin(cfg), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	return app
}

func adminTestConfig() models.AdminConfiguration {
	return models.AdminConfiguration{
		Username:      "admin",
		Password:      "secret",
		SessionSecret: "session-secret",
		SessionTTL:    time.Hour,
	}
}

func TestRequireAdmin_NoCookie_Unauthorized(t *testing.T) {
	app := newAdminApp(adminTestConfig())

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRequireAdmin_ValidCookie_Allows(t *testing.T) {
	cfg := adminTestConfig()
	app := newAdminApp(cfg)

	token := auth.IssueToken([]byte(cfg.SessionSecret), cfg.Username, cfg.SessionTTL)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})

	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRequireAdmin_ExpiredCookie_Unauthorized(t *testing.T) {
	cfg := adminTestConfig()
	app := newAdminApp(cfg)

	token := auth.IssueToken([]byte(cfg.SessionSecret), cfg.Username, -time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})

	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRequireAdmin_TamperedCookie_Unauthorized(t *testing.T) {
	cfg := adminTestConfig()
	app := newAdminApp(cfg)

	token := auth.IssueToken([]byte("different-secret"), cfg.Username, cfg.SessionTTL)
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})

	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
