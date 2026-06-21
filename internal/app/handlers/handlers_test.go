package handlers_test

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/app/handlers"
	"github.com/markhc/isrv/internal/database"
	dbmocks "github.com/markhc/isrv/internal/database/mocks"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	stmocks "github.com/markhc/isrv/internal/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/templates
var testTemplatesFS embed.FS

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func loadTemplates(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.New("").ParseFS(testTemplatesFS, "testdata/templates/*.tmpl")
	require.NoError(t, err)
	return tmpl
}

func defaultConfig() *models.Configuration {
	return &models.Configuration{
		ServerURL:      "http://localhost:8080",
		MaxFileSizeMB:  100,
		MinAgeDays:     30,
		MaxAgeDays:     365,
		RandomIDLength: 8,
		FaviconFormat:  "png",
	}
}

// newMockStorage wraps stmocks.NewMockStorage with an optional Backend()
// expectation so that handlers which read the backend label for metric
// attributes do not panic in tests that don't otherwise care about it.
func newMockStorage(t *testing.T) *stmocks.MockStorage {
	t.Helper()
	s := stmocks.NewMockStorage(t)
	s.On("Backend").Return("local").Maybe()
	return s
}

// newApp returns a fresh fiber.App with no middleware. Tests register the
// route under test on it before calling app.Test.
func newApp() *fiber.App {
	return fiber.New()
}

// doTest dispatches r through the app and returns the response. The caller
// is responsible for closing resp.Body.
func doTest(t *testing.T, app *fiber.App, r *http.Request) *http.Response {
	t.Helper()
	resp, err := app.Test(r, fiber.TestConfig{Timeout: 5 * time.Second, FailOnTimeout: true})
	require.NoError(t, err)
	return resp
}

// bodyString reads and returns the response body as a string, then closes it.
func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

// ---------------------------------------------------------------------------
// NotFound
// ---------------------------------------------------------------------------

func Test_NotFound(t *testing.T) {
	app := newApp()
	app.Use(handlers.NotFound(loadTemplates(t), defaultConfig()))

	resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/nonexistent", nil))
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

// ---------------------------------------------------------------------------
// Index
// ---------------------------------------------------------------------------

func Test_Index(t *testing.T) {
	app := newApp()
	app.Get("/", handlers.Index(loadTemplates(t), defaultConfig()))

	resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/", nil))
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

// ---------------------------------------------------------------------------
// Favicon
// ---------------------------------------------------------------------------

func Test_Favicon(t *testing.T) {
	faviconBytes := []byte{0x89, 0x50, 0x4E, 0x47}

	app := newApp()
	app.Get("/favicon.:format", handlers.Favicon(faviconBytes, "png"))

	resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/favicon.png", nil))
	body := bodyString(t, resp)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.NotEmpty(t, resp.Header.Get("Cache-Control"))
	assert.Equal(t, string(faviconBytes), body)
}

// ---------------------------------------------------------------------------
// Static
// ---------------------------------------------------------------------------

func Test_Static(t *testing.T) {
	staticDir, err := fs.Sub(testTemplatesFS, "testdata")
	require.NoError(t, err)

	app := newApp()
	app.Get("/static/*", handlers.Static(staticDir))

	t.Run("path traversal blocked", func(t *testing.T) {
		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/static/../webserver.go", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

// ---------------------------------------------------------------------------
// Download
// ---------------------------------------------------------------------------

func Test_Download(t *testing.T) {
	tests := []struct {
		name         string
		fileID       string
		fileName     string
		resolvedName string
		downloadErr  error
		metadata     map[string]string
	}{
		{
			name:         "happy path without filename",
			fileID:       "abc123",
			fileName:     "",
			resolvedName: "stored.png",
			metadata:     map[string]string{"Content-Type": "image/png"},
		},
		{
			name:         "happy path with filename",
			fileID:       "abc123",
			fileName:     "photo.png",
			resolvedName: "photo.png",
			metadata:     map[string]string{"Content-Type": "image/png"},
		},
		{
			name:         "OnFileDownload error is non-fatal",
			fileID:       "abc123",
			resolvedName: "stored.png",
			downloadErr:  errors.New("db error"),
			metadata:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			stor := newMockStorage(t)

			db.On("GetFileData", mock.Anything, tt.fileID).Return(&models.File{
				ID:       tt.fileID,
				Name:     tt.resolvedName,
				Metadata: tt.metadata,
			}, nil)
			db.On("OnFileDownload", mock.Anything, tt.fileID).Return(tt.downloadErr)
			stor.On("ServeFile", mock.Anything, tt.fileID, tt.resolvedName, tt.metadata, true, true).Return(nil)

			app := newApp()
			h := handlers.Download(db, stor)
			app.Get("/d/:id", h)
			app.Get("/d/:id/:filename", h)

			path := "/d/" + tt.fileID
			if tt.fileName != "" {
				path += "/" + tt.fileName
			}

			resp := doTest(t, app, httptest.NewRequest(http.MethodGet, path, nil))
			resp.Body.Close()
		})
	}
}

// ---------------------------------------------------------------------------
// Upload
// ---------------------------------------------------------------------------

func Test_Upload(t *testing.T) {
	multipartBody := func(t *testing.T, filename string, content []byte) (*bytes.Buffer, string) {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, _ = fw.Write(content)
		require.NoError(t, mw.Close())
		return &buf, mw.FormDataContentType()
	}

	tests := []struct {
		name           string
		setup          func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage)
		cfgMutator     func(*models.Configuration)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "missing file field returns 400",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return req, dbmocks.NewMockDatabase(t), newMockStorage(t)
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "file' field is missing",
		},
		{
			name: "file too large returns 413",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				body, ct := multipartBody(t, "big.bin", bytes.Repeat([]byte("x"), 10))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				return req, dbmocks.NewMockDatabase(t), newMockStorage(t)
			},
			cfgMutator:     func(c *models.Configuration) { c.MaxFileSizeMB = 0 },
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectedBody:   "file size exceeds the maximum allowed limit",
		},
		{
			name: "SaveFileUpload error returns 500",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				body, ct := multipartBody(t, "file.txt", []byte("hello"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := dbmocks.NewMockDatabase(t)
				stor := newMockStorage(t)
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", errors.New("storage failure"))
				return req, db, stor
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to process upload",
		},
		{
			name: "OnFileUpload error rolls back storage and returns 500",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				body, ct := multipartBody(t, "file.txt", []byte("hello"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := dbmocks.NewMockDatabase(t)
				stor := newMockStorage(t)
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("/path/file.txt", nil)
				db.On("OnFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("db error"))
				stor.On("DeleteFile", mock.Anything, mock.Anything).Return(nil)
				return req, db, stor
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to process upload",
		},
		{
			name: "happy path returns 200 with URL",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				body, ct := multipartBody(t, "photo.png", []byte("image data"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := dbmocks.NewMockDatabase(t)
				stor := newMockStorage(t)
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("/path/photo.png", nil)
				db.On("OnFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil)
				return req, db, stor
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"status":"success"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := defaultConfig()
			if tt.cfgMutator != nil {
				tt.cfgMutator(cfg)
			}

			req, db, stor := tt.setup(t)

			app := newApp()
			app.Post("/", handlers.Upload(cfg, db, stor))

			resp := doTest(t, app, req)
			body := bodyString(t, resp)

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			assert.Contains(t, body, tt.expectedBody)
		})
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func Test_Delete(t *testing.T) {
	tests := []struct {
		name           string
		fileID         string
		setup          func(db *dbmocks.MockDatabase, st *stmocks.MockStorage)
		expectedStatus int
		expectedBody   string
	}{
		{
			name:   "happy path returns 204",
			fileID: "abc123",
			setup: func(db *dbmocks.MockDatabase, st *stmocks.MockStorage) {
				st.On("DeleteFile", mock.Anything, "abc123").Return(nil)
				db.On("OnFileDelete", mock.Anything, "abc123").Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:   "storage error returns 500",
			fileID: "abc123",
			setup: func(db *dbmocks.MockDatabase, st *stmocks.MockStorage) {
				st.On("DeleteFile", mock.Anything, "abc123").Return(errors.New("disk full"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to delete file",
		},
		{
			name:   "database error after storage deletion returns 500",
			fileID: "abc123",
			setup: func(db *dbmocks.MockDatabase, st *stmocks.MockStorage) {
				st.On("DeleteFile", mock.Anything, "abc123").Return(nil)
				db.On("OnFileDelete", mock.Anything, "abc123").Return(errors.New("db error"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to delete file record",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			st := newMockStorage(t)
			tt.setup(db, st)

			app := newApp()
			app.Delete("/:id", handlers.Delete(db, st))

			req := httptest.NewRequest(http.MethodDelete, "/"+tt.fileID, nil)
			resp := doTest(t, app, req)
			body := bodyString(t, resp)

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			if tt.expectedBody != "" {
				assert.Contains(t, body, tt.expectedBody)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Expire
// ---------------------------------------------------------------------------

// smallFileRecord returns a File for a 1 KB file, whose max expiry
// (relative to defaultConfig) is ~maxAgeDays from now.
func smallFileRecord() *models.File {
	return &models.File{
		ID:         "abc123",
		Name:       "file.txt",
		Size:       1024,
		Expiration: time.Now().Add(30 * 24 * time.Hour),
	}
}

func Test_Expire(t *testing.T) {
	expiresJSON := func(expires string) string {
		return fmt.Sprintf(`{"expires":%q}`, expires)
	}
	unixMsIn := func(d time.Duration) string {
		return fmt.Sprintf("%d", time.Now().Add(d).UnixMilli())
	}

	tests := []struct {
		name           string
		fileID         string
		body           string
		setup          func(db *dbmocks.MockDatabase)
		expectedStatus int
		expectedBody   string
	}{
		{
			name:   "happy path — unix-ms timestamp within limit",
			fileID: "abc123",
			body:   expiresJSON(unixMsIn(24 * time.Hour)),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", mock.Anything, "abc123").Return(smallFileRecord(), nil)
				db.On("SetExpiration", mock.Anything, "abc123", mock.AnythingOfType("time.Time")).Return(nil)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"fileId":"abc123"`,
		},
		{
			name:   "happy path — hours shorthand within limit",
			fileID: "abc123",
			body:   expiresJSON("48"),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", mock.Anything, "abc123").Return(smallFileRecord(), nil)
				db.On("SetExpiration", mock.Anything, "abc123", mock.AnythingOfType("time.Time")).Return(nil)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"expiration"`,
		},
		{
			name:           "missing expires field returns 400",
			fileID:         "abc123",
			body:           `{}`,
			setup:          func(_ *dbmocks.MockDatabase) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "'expires' field is required",
		},
		{
			name:           "non-JSON body returns 400",
			fileID:         "abc123",
			body:           `not json`,
			setup:          func(_ *dbmocks.MockDatabase) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "invalid request body",
		},
		{
			name:           "non-numeric expires value returns 400",
			fileID:         "abc123",
			body:           `{"expires":"next-tuesday"}`,
			setup:          func(_ *dbmocks.MockDatabase) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "invalid expires value",
		},
		{
			name:   "expiry exceeding max returns 422",
			fileID: "abc123",
			body:   expiresJSON(unixMsIn(400 * 24 * time.Hour)),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", mock.Anything, "abc123").Return(smallFileRecord(), nil)
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedBody:   "expiration exceeds the maximum allowed time",
		},
		{
			name:   "file not found returns 404",
			fileID: "missing",
			body:   expiresJSON("24"),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", mock.Anything, "missing").Return(nil, database.ErrFileNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   "file not found",
		},
		{
			name:   "database error on GetFileData returns 500",
			fileID: "abc123",
			body:   expiresJSON("24"),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", mock.Anything, "abc123").Return(nil, database.ErrDatabase)
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "internal server error",
		},
		{
			name:   "database error on SetExpiration returns 500",
			fileID: "abc123",
			body:   expiresJSON(unixMsIn(24 * time.Hour)),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", mock.Anything, "abc123").Return(smallFileRecord(), nil)
				db.On("SetExpiration", mock.Anything, "abc123", mock.AnythingOfType("time.Time")).Return(errors.New("db error"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to update expiration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			tt.setup(db)

			app := newApp()
			app.Patch("/:id/expire", handlers.Expire(defaultConfig(), db))

			req := httptest.NewRequest(http.MethodPatch, "/"+tt.fileID+"/expire", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp := doTest(t, app, req)
			body := bodyString(t, resp)

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			if tt.expectedBody != "" {
				assert.Contains(t, body, tt.expectedBody)
			}
		})
	}
}
