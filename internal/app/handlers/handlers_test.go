package handlers_test

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"
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

// chiRequest injects chi URL params into a request's context.
func chiRequest(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ---------------------------------------------------------------------------
// NotFound
// ---------------------------------------------------------------------------

func Test_NotFound(t *testing.T) {
	tmpl := loadTemplates(t)
	h := handlers.NotFound(tmpl, defaultConfig())

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()

	h(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

// ---------------------------------------------------------------------------
// Index
// ---------------------------------------------------------------------------

func Test_Index(t *testing.T) {
	tmpl := loadTemplates(t)
	h := handlers.Index(tmpl, defaultConfig())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	h(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

// ---------------------------------------------------------------------------
// Favicon
// ---------------------------------------------------------------------------

func Test_Favicon(t *testing.T) {
	faviconBytes := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes
	h := handlers.Favicon(faviconBytes, "png")

	req := httptest.NewRequest(http.MethodGet, "/favicon.png", nil)
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.NotEmpty(t, resp.Header.Get("cache-control"))
	assert.Equal(t, faviconBytes, w.Body.Bytes())
}

// ---------------------------------------------------------------------------
// Static
// ---------------------------------------------------------------------------

func Test_Static(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{
			name:           "path traversal blocked",
			path:           "/static/../webserver.go",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			staticDir, err := fs.Sub(testTemplatesFS, "testdata")
			require.NoError(t, err)

			h := handlers.Static(staticDir)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req = chiRequest(req, map[string]string{
				"file": strings.TrimPrefix(tt.path, "/static/"),
			})
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

// ---------------------------------------------------------------------------
// Download
// ---------------------------------------------------------------------------

func Test_Download(t *testing.T) {
	tests := []struct {
		name        string
		fileID      string
		fileName    string
		downloadErr error
		metadata    map[string]string
		metadataErr error
	}{
		{
			name:     "happy path without filename",
			fileID:   "abc123",
			fileName: "",
			metadata: map[string]string{"Content-Type": "image/png"},
		},
		{
			name:     "happy path with filename",
			fileID:   "abc123",
			fileName: "photo.png",
			metadata: map[string]string{"Content-Type": "image/png"},
		},
		{
			name:        "OnFileDownload error is non-fatal",
			fileID:      "abc123",
			downloadErr: errors.New("db error"),
			metadata:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			stor := stmocks.NewMockStorage(t)

			db.On("OnFileDownload", tt.fileID).Return(tt.downloadErr)
			db.On("GetFileMetadata", tt.fileID).Return(tt.metadata, tt.metadataErr)
			stor.On("ServeFile", mock.Anything, mock.Anything, tt.fileID, tt.fileName, tt.metadata, true, true).Return()

			h := handlers.Download(db, stor)

			path := "/d/" + tt.fileID
			params := map[string]string{"id": tt.fileID}
			if tt.fileName != "" {
				path += "/" + tt.fileName
				params["filename"] = tt.fileName
			}

			req := httptest.NewRequest(http.MethodGet, path, nil)
			req = chiRequest(req, params)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)
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
		fw.Write(content)
		mw.Close()
		return &buf, mw.FormDataContentType()
	}

	tests := []struct {
		name           string
		setup          func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "missing file field returns 400",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return req, dbmocks.NewMockDatabase(t), stmocks.NewMockStorage(t)
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
				return req, dbmocks.NewMockDatabase(t), stmocks.NewMockStorage(t)
			},
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
				stor := stmocks.NewMockStorage(t)
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", errors.New("storage failure"))
				return req, db, stor
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to process upload",
		},
		{
			name: "OnFileUpload error is non-fatal",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				body, ct := multipartBody(t, "file.txt", []byte("hello"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := dbmocks.NewMockDatabase(t)
				stor := stmocks.NewMockStorage(t)
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("/path/file.txt", nil)
				db.On("OnFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("db error"))
				return req, db, stor
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"status":"success"`,
		},
		{
			name: "happy path returns 200 with URL",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				body, ct := multipartBody(t, "photo.png", []byte("image data"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := dbmocks.NewMockDatabase(t)
				stor := stmocks.NewMockStorage(t)
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("/path/photo.png", nil)
				db.On("OnFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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
			if tt.name == "file too large returns 413" {
				cfg.MaxFileSizeMB = 0
			}

			req, db, stor := tt.setup(t)
			h := handlers.Upload(cfg, db, stor)

			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), tt.expectedBody)
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
				db.On("OnFileDelete", "abc123").Return(nil)
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
				db.On("OnFileDelete", "abc123").Return(errors.New("db error"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to delete file record",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			st := stmocks.NewMockStorage(t)
			tt.setup(db, st)

			h := handlers.Delete(db, st)
			req := chiRequest(httptest.NewRequest(http.MethodDelete, "/"+tt.fileID, nil), map[string]string{"id": tt.fileID})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.Contains(t, w.Body.String(), tt.expectedBody)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Expire
// ---------------------------------------------------------------------------

// smallFileRecord returns a FileRecord for a 1 KB file, whose max expiry
// (relative to defaultConfig) is ~maxAgeDays from now.
func smallFileRecord() *database.FileRecord {
	return &database.FileRecord{
		ID:             "abc123",
		FileSize:       1024, // 1 KB — effectively treated as zero size → max age applies
		ExpirationTime: time.Now().Add(30 * 24 * time.Hour),
	}
}

func Test_Expire(t *testing.T) {
	// expiresJSON returns a full JSON body for the given expires value string.
	expiresJSON := func(expires string) string {
		return fmt.Sprintf(`{"expires":%q}`, expires)
	}
	// unixMsIn returns a Unix-ms string for a time offset from now.
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
			body:   expiresJSON(unixMsIn(24 * time.Hour)), // 1 day from now — well within 30 days min
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", "abc123").Return(smallFileRecord(), nil)
				db.On("SetExpiration", "abc123", mock.AnythingOfType("time.Time")).Return(nil)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"fileId":"abc123"`,
		},
		{
			name:   "happy path — hours shorthand within limit",
			fileID: "abc123",
			body:   expiresJSON("48"), // 48 hours from now
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", "abc123").Return(smallFileRecord(), nil)
				db.On("SetExpiration", "abc123", mock.AnythingOfType("time.Time")).Return(nil)
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
			// A 1 KB file with defaultConfig (min=30d, max=365d) has a max expiry ~365 days;
			// request 400 days — exceeds the max for a 1 KB file under defaultConfig.
			body: expiresJSON(unixMsIn(400 * 24 * time.Hour)),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", "abc123").Return(smallFileRecord(), nil)
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedBody:   "expiration exceeds the maximum allowed time",
		},
		{
			name:   "file not found returns 404",
			fileID: "missing",
			body:   expiresJSON("24"),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", "missing").Return(nil, database.ErrFileNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   "file not found",
		},
		{
			name:   "database error on GetFileData returns 500",
			fileID: "abc123",
			body:   expiresJSON("24"),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", "abc123").Return(nil, database.ErrDatabase)
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "internal server error",
		},
		{
			name:   "database error on SetExpiration returns 500",
			fileID: "abc123",
			body:   expiresJSON(unixMsIn(24 * time.Hour)),
			setup: func(db *dbmocks.MockDatabase) {
				db.On("GetFileData", "abc123").Return(smallFileRecord(), nil)
				db.On("SetExpiration", "abc123", mock.AnythingOfType("time.Time")).Return(errors.New("db error"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to update expiration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			tt.setup(db)

			h := handlers.Expire(defaultConfig(), db)
			req := chiRequest(
				httptest.NewRequest(http.MethodPatch, "/"+tt.fileID+"/expire", strings.NewReader(tt.body)),
				map[string]string{"id": tt.fileID},
			)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.Contains(t, w.Body.String(), tt.expectedBody)
			}
		})
	}
}
