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
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{name: "upload", method: http.MethodPost, path: "/", wantStatus: http.StatusCreated},
		{name: "delete", method: http.MethodDelete, path: "/file-id", wantStatus: http.StatusNoContent},
		{name: "expire", method: http.MethodPatch, path: "/file-id/expire", wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			sendRequest := func(method, path string) int {
				response, err := server.Test(httptest.NewRequest(method, path, nil), fiber.TestConfig{
					Timeout:       5 * time.Second,
					FailOnTimeout: true,
				})
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				return response.StatusCode
			}

			assert.Equal(t, http.StatusOK, sendRequest(http.MethodGet, "/assets/index.js"))
			assert.Equal(t, tt.wantStatus, sendRequest(tt.method, tt.path))
			assert.Equal(t, http.StatusTooManyRequests, sendRequest(tt.method, tt.path))
		})
	}
}
