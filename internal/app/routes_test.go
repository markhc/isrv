package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	appmiddleware "github.com/markhc/isrv/internal/app/middleware"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

func TestSetupRoutes_StaticAssetsDoNotConsumeRateLimitBudget(t *testing.T) {
	rateLimit := appmiddleware.RateLimit(t.Context(), models.RateLimitConfiguration{
		Enabled:           true,
		RequestsPerMinute: 1,
		BurstSize:         1,
		OnLimitExceeded:   models.RateLimitActionThrottle,
	}, models.ClusterConfiguration{})

	a := &Application{
		DownloadHandler: func(c fiber.Ctx) error {
			return c.SendStatus(fiber.StatusOK)
		},
		UploadHandler: func(c fiber.Ctx) error {
			return c.SendStatus(fiber.StatusCreated)
		},
		DeleteHandler: func(c fiber.Ctx) error {
			return c.SendStatus(fiber.StatusNoContent)
		},
		ExpireHandler: func(c fiber.Ctx) error {
			return c.SendStatus(fiber.StatusNoContent)
		},
		SPAHandler: func(c fiber.Ctx) error {
			return c.SendStatus(fiber.StatusOK)
		},
		Middleware: AppMiddleware{
			RateLimit: rateLimit,
			RequireToken: func(c fiber.Ctx) error {
				return c.Next()
			},
		},
	}

	server := fiber.New()
	SetupRoutes(server, a)

	assetResponse, err := server.Test(httptest.NewRequest(http.MethodGet, "/assets/index.js", nil), fiber.TestConfig{
		Timeout:       5 * time.Second,
		FailOnTimeout: true,
	})
	require.NoError(t, err)
	require.NoError(t, assetResponse.Body.Close())
	assert.Equal(t, http.StatusOK, assetResponse.StatusCode)

	firstUpload, err := server.Test(httptest.NewRequest(http.MethodPost, "/", nil), fiber.TestConfig{
		Timeout:       5 * time.Second,
		FailOnTimeout: true,
	})
	require.NoError(t, err)
	require.NoError(t, firstUpload.Body.Close())
	assert.Equal(t, http.StatusCreated, firstUpload.StatusCode)

	secondUpload, err := server.Test(httptest.NewRequest(http.MethodPost, "/", nil), fiber.TestConfig{
		Timeout:       5 * time.Second,
		FailOnTimeout: true,
	})
	require.NoError(t, err)
	require.NoError(t, secondUpload.Body.Close())
	assert.Equal(t, http.StatusTooManyRequests, secondUpload.StatusCode)
}
