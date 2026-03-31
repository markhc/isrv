package headers

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_AddCacheHeader(t *testing.T) {
	recorder := httptest.NewRecorder()

	AddCacheHeader(recorder)

	expectedHeaders := map[string]string{
		"cdn-cache-control":            "public, max-age=36000",
		"Cloudflare-CDN-Cache-Control": "public, max-age=36000",
		"cache-control":                "public, max-age=36000",
	}

	for headerName, expectedValue := range expectedHeaders {
		assert.Equal(t, expectedValue, recorder.Header().Get(headerName))
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
			recorder := httptest.NewRecorder()

			SetContentDisposition(recorder, tt.fileName, tt.inline)
			assert.Equal(t, tt.expected, recorder.Header().Get("Content-Disposition"))
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
		{"empty content type", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			SetContentType(recorder, tt.contentType)
			assert.Equal(t, tt.contentType, recorder.Header().Get("Content-Type"))
		})
	}
}

func Test_SetHeaders_withMetadata(t *testing.T) {
	recorder := httptest.NewRecorder()
	fileName := "test.pdf"
	fileMetadata := map[string]string{
		"Content-Type": "application/pdf",
	}

	SetHeaders(recorder, fileName, fileMetadata, false, true)

	assert.Equal(t, "public, max-age=36000", recorder.Header().Get("cache-control"))
	assert.Equal(t, "application/pdf", recorder.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="test.pdf"`, recorder.Header().Get("Content-Disposition"))
}

func Test_SetHeaders_noCache(t *testing.T) {
	recorder := httptest.NewRecorder()
	fileName := "image.jpg"
	fileMetadata := map[string]string{
		"Content-Type": "image/jpeg",
	}

	SetHeaders(recorder, fileName, fileMetadata, true, false)

	assert.Empty(t, recorder.Header().Get("cache-control"))
	assert.Equal(t, "image/jpeg", recorder.Header().Get("Content-Type"))
	assert.Equal(t, `inline; filename="image.jpg"`, recorder.Header().Get("Content-Disposition"))
}

func Test_SetHeaders_noMetadata(t *testing.T) {
	recorder := httptest.NewRecorder()
	fileName := "unknown.bin"
	fileMetadata := map[string]string{} // empty metadata

	SetHeaders(recorder, fileName, fileMetadata, false, false)

	assert.Empty(t, recorder.Header().Get("Content-Type"))
	assert.Equal(t, `attachment; filename="unknown.bin"`, recorder.Header().Get("Content-Disposition"))
}
