package headers

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCtx executes fn inside a request handler so it has a live fiber.Ctx,
// then returns the response headers captured by app.Test.
func runCtx(t *testing.T, fn func(c fiber.Ctx)) map[string]string {
	t.Helper()

	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		fn(c)
		return nil
	})

	req := httptest.NewRequest("GET", "/", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	out := make(map[string]string)
	for k := range resp.Header {
		out[k] = resp.Header.Get(k)
	}
	return out
}

func Test_AddCacheHeader(t *testing.T) {
	got := runCtx(t, AddCacheHeader)

	expectedHeaders := map[string]string{
		"Cdn-Cache-Control":            "public, max-age=36000",
		"Cloudflare-Cdn-Cache-Control": "public, max-age=36000",
		"Cache-Control":                "public, max-age=36000",
	}

	for headerName, expectedValue := range expectedHeaders {
		assert.Equal(t, expectedValue, got[headerName], "header %s", headerName)
	}
}

func Test_SetContentDisposition(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		inline   bool
		expected string
	}{
		{"attachment simple filename", "document.pdf", false, `attachment; filename="document.pdf"`},
		{"attachment with spaces", "my file.txt", false, `attachment; filename="my file.txt"`},
		{"attachment with special chars", "file-name_v2.docx", false, `attachment; filename="file-name_v2.docx"`},
		{"attachment empty filename", "", false, `attachment; filename=""`},
		{"inline simple filename", "image.jpg", true, `inline; filename="image.jpg"`},
		{"inline with spaces", "photo gallery.png", true, `inline; filename="photo gallery.png"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCtx(t, func(c fiber.Ctx) {
				SetContentDisposition(c, tt.fileName, tt.inline)
			})
			assert.Equal(t, tt.expected, got["Content-Disposition"])
		})
	}
}

func Test_SetContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
	}{
		{"text/plain", "text/plain"},
		{"application/json", "application/json"},
		{"image/jpeg", "image/jpeg"},
		{"application/octet-stream", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCtx(t, func(c fiber.Ctx) {
				SetContentType(c, tt.contentType)
			})
			assert.Equal(t, tt.contentType, got["Content-Type"])
		})
	}
}

func Test_SetHeaders_withMetadata(t *testing.T) {
	got := runCtx(t, func(c fiber.Ctx) {
		SetHeaders(c, "test.pdf", map[string]string{"Content-Type": "application/pdf"}, false, true)
	})

	assert.Equal(t, "public, max-age=36000", got["Cache-Control"])
	assert.Equal(t, "application/pdf", got["Content-Type"])
	assert.Equal(t, `attachment; filename="test.pdf"`, got["Content-Disposition"])
}

func Test_SetHeaders_noCache(t *testing.T) {
	got := runCtx(t, func(c fiber.Ctx) {
		SetHeaders(c, "image.jpg", map[string]string{"Content-Type": "image/jpeg"}, true, false)
	})

	assert.Empty(t, got["Cache-Control"])
	assert.Equal(t, "image/jpeg", got["Content-Type"])
	assert.Equal(t, `inline; filename="image.jpg"`, got["Content-Disposition"])
}

func Test_SetHeaders_noMetadata(t *testing.T) {
	got := runCtx(t, func(c fiber.Ctx) {
		SetHeaders(c, "unknown.bin", map[string]string{}, false, false)
	})

	assert.Equal(t, `attachment; filename="unknown.bin"`, got["Content-Disposition"])
}
