package handlers

import (
	"bytes"
	"text/template"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
)

// NotFound returns a handler that renders the 404 page and responds with HTTP 404.
func NotFound(tmpl *template.Template, config *models.Configuration) fiber.Handler {
	return func(c fiber.Ctx) error {
		return renderTemplate(c, tmpl, "notfound", config, fiber.StatusNotFound)
	}
}

// Index returns a handler that renders the index page.
func Index(tmpl *template.Template, config *models.Configuration) fiber.Handler {
	return func(c fiber.Ctx) error {
		logging.DebugCtx(c.Context(), "indexHandler", logging.String("path", c.Path()))
		return renderTemplate(c, tmpl, "index", config, fiber.StatusOK)
	}
}

func renderTemplate(c fiber.Ctx, tmpl *template.Template, name string, config *models.Configuration, status int) error {
	data := struct {
		Config *models.Configuration
	}{Config: config}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		logging.ErrorCtx(c.Context(), "failed to execute template", logging.Error(err))
		return c.Status(fiber.StatusInternalServerError).SendString("internal server error")
	}

	c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
	return c.Status(status).Send(buf.Bytes())
}
