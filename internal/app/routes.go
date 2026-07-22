package app

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/google/uuid"
	"github.com/markhc/isrv/internal/logging"
)

// SetupRoutes registers all application routes, handlers, and middleware.
func SetupRoutes(app *fiber.App, a *Application) {
	app.Use(requestid.New(requestid.Config{
		Header: fiber.HeaderXRequestID,
		Generator: func() string {
			id, _ := uuid.NewV7()
			return id.String()
		},
	}))

	// Request logger.
	app.Use(logging.RequestLogger(&logging.RequestLoggerOptions{
		SkipFunc: func(c fiber.Ctx, _ int) bool {
			return c.Method() == fiber.MethodOptions
		},
	}))

	// Panic recovery that logs the panic before allowing Fiber's recover
	// middleware to translate it to a 500.
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c fiber.Ctx, e any) {
			logging.ErrorCtx(c.Context(), "request handler panic",
				logging.Any("panic", e),
			)
		},
	}))

	if a.FaviconHandler != nil {
		app.Get("/favicon.:format", a.FaviconHandler)
	}

	app.Get("/d/:id", a.DownloadHandler)
	app.Get("/d/:id/:filename", a.DownloadHandler)

	// Rate limit mutation endpoints without placing middleware in front of the
	// SPA catch-all, which also serves static assets.
	app.Post("/", a.Middleware.RateLimit, a.UploadHandler)
	app.Delete("/:id", a.Middleware.RateLimit, a.Middleware.RequireToken, a.DeleteHandler)
	app.Patch("/:id/expire", a.Middleware.RateLimit, a.Middleware.RequireToken, a.ExpireHandler)

	// Operational endpoints.
	if a.HealthzHandler != nil {
		app.Get("/healthz", a.HealthzHandler)
	}
	if a.ReadyzHandler != nil {
		app.Get("/readyz", a.ReadyzHandler)
	}
	if a.MetricsHandler != nil {
		app.Get("/metrics", adaptor.HTTPHandler(a.MetricsHandler))
	}

	adminAPI := app.Group("/admin/api")
	if a.AdminEnabled {
		adminAPI.Post("/login", a.Middleware.RateLimit, a.Middleware.AdminRateLimit, a.AdminLoginHandler)
		adminAPI.Post("/logout", a.AdminLogoutHandler)
		adminAPI.Get("/session", a.AdminSessionHandler)
		adminAPI.Get("/files", a.Middleware.RequireAdmin, a.AdminListHandler)
		adminAPI.Delete("/files/:id", a.Middleware.RequireAdmin, a.AdminDeleteHandler)
	} else {
		adminAPI.Use(func(c fiber.Ctx) error {
			return c.Status(fiber.StatusNotFound).JSON(map[string]string{"error": "not found"})
		})
	}

	// SPA catch-all: serves index.html for any unmatched path so that
	// client-side routing works. Must be registered last.
	if a.SPAHandler != nil {
		app.Use(a.SPAHandler)
	}
}
