// Package headers provides small helpers for setting common HTTP response
// headers used when serving file downloads.
package headers

import (
	"mime"

	"github.com/gofiber/fiber/v3"
)

// AddCacheHeader sets long-lived (10h) cache-control headers, including
// CDN-specific variants understood by Cloudflare.
func AddCacheHeader(c fiber.Ctx) {
	c.Set("cdn-cache-control", "public, max-age=36000")
	c.Set("Cloudflare-CDN-Cache-Control", "public, max-age=36000")
	c.Set("cache-control", "public, max-age=36000")
}

// SetContentDisposition sets the Content-Disposition header. It uses
// "inline" when inlineContent is true and "attachment" otherwise.
func SetContentDisposition(c fiber.Ctx, fileName string, inlineContent bool) {
	dispositionType := "attachment"
	if inlineContent {
		dispositionType = "inline"
	}

	value := mime.FormatMediaType(dispositionType, map[string]string{"filename": fileName})
	if value == "" {
		value = dispositionType
	}

	c.Set("Content-Disposition", value)
}

// SetContentType sets the Content-Type header on the response.
func SetContentType(c fiber.Ctx, contentType string) {
	c.Set("Content-Type", contentType)
}

// SetUserContentSecurityHeaders isolates uploaded content from the application
// origin. The CSP sandbox gives the response an opaque origin, so uploaded HTML
// cannot run script as isrv, read its storage, or register a service worker
// that would intercept share links. nosniff stops browsers from reinterpreting
// the declared content type.
func SetUserContentSecurityHeaders(c fiber.Ctx) {
	c.Set("Content-Security-Policy", "sandbox")
	c.Set("X-Content-Type-Options", "nosniff")
}

// SetHeaders applies the user-content security headers, cache-control (when
// enabled), content-type (from fileMetadata), and content-disposition headers
// in a single call.
func SetHeaders(
	c fiber.Ctx,
	fileName string,
	contentType string,
	inlineContent bool,
	cachingEnabled bool,
) {
	SetUserContentSecurityHeaders(c)

	if cachingEnabled {
		AddCacheHeader(c)
	}

	if contentType != "" {
		SetContentType(c, contentType)
	}

	SetContentDisposition(c, fileName, inlineContent)
}
