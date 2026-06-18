// Package headers provides small helpers for setting common HTTP response
// headers used when serving file downloads.
package headers

import "github.com/gofiber/fiber/v3"

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
	c.Set("Content-Disposition", dispositionType+"; filename=\""+fileName+"\"")
}

// SetContentType sets the Content-Type header on the response.
func SetContentType(c fiber.Ctx, contentType string) {
	c.Set("Content-Type", contentType)
}

// SetHeaders applies the cache-control (when enabled), content-type (from
// fileMetadata), and content-disposition headers in a single call.
func SetHeaders(
	c fiber.Ctx,
	fileName string,
	fileMetadata map[string]string,
	inlineContent bool,
	cachingEnabled bool,
) {
	if cachingEnabled {
		AddCacheHeader(c)
	}

	if contentType, ok := fileMetadata["Content-Type"]; ok {
		SetContentType(c, contentType)
	}

	SetContentDisposition(c, fileName, inlineContent)
}
