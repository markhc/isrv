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
