package headers

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/models"
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
		{"attachment simple filename", "document.pdf", false, `attachment; filename=document.pdf`},
		{"attachment with spaces", "my file.txt", false, `attachment; filename="my file.txt"`},
		{"attachment with special chars", "file-name_v2.docx", false, `attachment; filename=file-name_v2.docx`},
		{"attachment empty filename", "", false, `attachment; filename=""`},
		{"inline simple filename", "image.jpg", true, `inline; filename=image.jpg`},
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

func Test_SetHeaders(t *testing.T) {
	tests := []struct {
		name            string
		file            models.File
		inline          bool
		cache           bool
		expectedHeaders map[string]string
		absentHeaders   []string
	}{
		{
			name: "with metadata",
			file: models.File{
				ID:          "test-id",
				Name:        "test.pdf",
				ContentType: "application/pdf",
			},
			inline: false,
			cache:  true,
			expectedHeaders: map[string]string{
				"Cache-Control":       "public, max-age=36000",
				"Content-Type":        "application/pdf",
				"Content-Disposition": `attachment; filename=test.pdf`,
			},
		},
		{
			name: "with spaces in filename",
			file: models.File{
				ID:          "test-id",
				Name:        "my file.txt",
				ContentType: "application/pdf",
			},
			inline: false,
			cache:  true,
			expectedHeaders: map[string]string{
				"Cache-Control":       "public, max-age=36000",
				"Content-Type":        "application/pdf",
				"Content-Disposition": `attachment; filename="my file.txt"`,
			},
		},
		{
			name: "no cache",
			file: models.File{
				ID:          "test-id",
				Name:        "image.jpg",
				ContentType: "image/jpeg",
			},
			inline: true,
			cache:  false,
			expectedHeaders: map[string]string{
				"Content-Type":        "image/jpeg",
				"Content-Disposition": `inline; filename=image.jpg`,
			},
			absentHeaders: []string{"Cache-Control"},
		},
		{
			name: "no metadata",
			file: models.File{
				ID:          "test-id",
				Name:        "unknown.bin",
				ContentType: "application/octet-stream",
			},
			inline: false,
			cache:  false,
			expectedHeaders: map[string]string{
				"Content-Disposition": `attachment; filename=unknown.bin`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCtx(t, func(c fiber.Ctx) {
				SetHeaders(c, tt.file.Name, tt.file.ContentType, tt.inline, tt.cache)
			})

			for header, expected := range tt.expectedHeaders {
				assert.Equal(t, expected, got[header], "header %s", header)
			}
			for _, header := range tt.absentHeaders {
				assert.Empty(t, got[header], "header %s should be absent", header)
			}
		})
	}
}
