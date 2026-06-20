package handlers

import (
	"io/fs"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/headers"
	"github.com/markhc/isrv/internal/logging"
)

// Static returns a handler that serves embedded static files and rejects
// path-traversal attempts. The route should capture the requested file via
// a wildcard parameter named "*".
func Static(staticFilesDir fs.FS) fiber.Handler {
	return func(c fiber.Ctx) error {
		path := c.Params("*")
		logging.DebugCtx(c.Context(), "staticFilesHandler", logging.String("path", c.Path()))

		if path == "" || strings.Contains(path, "..") {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid file path")
		}

		data, err := fs.ReadFile(staticFilesDir, path)
		if err != nil {
			return c.Status(fiber.StatusNotFound).SendString("not found")
		}

		headers.AddCacheHeader(c)

		// Determine content type from extension; Fiber will infer from filename.
		c.Type(extOf(path))
		return c.Send(data)
	}
}

// SPA returns a handler that serves a single-page application from distFS.
// Requests for files that exist in the FS (e.g. /assets/...) are served
// directly with cache headers. All other requests fall back to index.html so
// client-side routing can handle them.
func SPA(distFS fs.FS) fiber.Handler {
	indexHTML, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		// Frontend has not been built yet — serve a minimal placeholder.
		return func(c fiber.Ctx) error {
			c.Set(fiber.HeaderContentType, "text/plain; charset=utf-8")
			return c.Status(fiber.StatusServiceUnavailable).SendString("frontend not built: run 'make frontend'")
		}
	}

	serveIndex := func(c fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "text/html; charset=utf-8")
		return c.Send(indexHTML)
	}

	return func(c fiber.Ctx) error {
		path := strings.TrimPrefix(c.Path(), "/")

		if path == "" {
			return serveIndex(c)
		}

		if strings.Contains(path, "..") {
			return c.Status(fiber.StatusBadRequest).SendString("invalid path")
		}

		data, err := fs.ReadFile(distFS, path)
		if err != nil {
			// Unknown path — let the SPA router handle it.
			return serveIndex(c)
		}

		headers.AddCacheHeader(c)
		c.Type(extOf(path))
		return c.Send(data)
	}
}

// Favicon returns a handler that serves the pre-fetched favicon bytes.
func Favicon(data []byte, format string) fiber.Handler {
	return func(c fiber.Ctx) error {
		headers.AddCacheHeader(c)
		headers.SetContentType(c, "image/"+format)
		return c.Send(data)
	}
}

// extOf returns the lowercase extension (without the leading dot) of path,
// or an empty string if the path has no extension.
func extOf(path string) string {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return ""
	}
	return strings.ToLower(path[i+1:])
}
