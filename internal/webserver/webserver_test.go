package webserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type MockDB struct{ mock.Mock }

func (m *MockDB) Connect() error { return m.Called().Error(0) }
func (m *MockDB) Close() error   { return m.Called().Error(0) }
func (m *MockDB) Migrate() error { return m.Called().Error(0) }
func (m *MockDB) OnFileUpload(fileID string, h *multipart.FileHeader, exp time.Time, ip string) error {
	return m.Called(fileID, h, exp, ip).Error(0)
}
func (m *MockDB) OnFileDownload(fileID string) error { return m.Called(fileID).Error(0) }
func (m *MockDB) OnFileDelete(fileID string) error   { return m.Called(fileID).Error(0) }
func (m *MockDB) GetFileMetadata(fileID string) (map[string]string, error) {
	args := m.Called(fileID)
	md, _ := args.Get(0).(map[string]string)
	return md, args.Error(1)
}
func (m *MockDB) GetExpiredFiles() ([]string, error) {
	args := m.Called()
	files, _ := args.Get(0).([]string)
	return files, args.Error(1)
}

type MockStorage struct{ mock.Mock }

func (m *MockStorage) FileExists(ctx context.Context, fileID string) (bool, error) {
	args := m.Called(ctx, fileID)
	return args.Bool(0), args.Error(1)
}
func (m *MockStorage) SaveFileUpload(ctx context.Context, fileID string, file multipart.File, h *multipart.FileHeader) (string, error) {
	args := m.Called(ctx, fileID, file, h)
	return args.String(0), args.Error(1)
}
func (m *MockStorage) DeleteFile(ctx context.Context, fileID string) error {
	return m.Called(ctx, fileID).Error(0)
}
func (m *MockStorage) ServeFile(w http.ResponseWriter, r *http.Request, fileID, fileName string, metadata map[string]string, inline, caching bool) {
	m.Called(w, r, fileID, fileName, metadata, inline, caching)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestServer(t *testing.T, cfg *models.Configuration, db *MockDB, stor *MockStorage) *server {
	t.Helper()
	tmpl, err := initializeTemplates(templatesFolderEmbedded)
	if err != nil {
		t.Fatalf("initializeTemplates: %v", err)
	}
	return &server{config: cfg, db: db, storage: stor, templates: tmpl}
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

// ---------------------------------------------------------------------------
// handler404
// ---------------------------------------------------------------------------

func Test_handler404(t *testing.T) {
	srv := newTestServer(t, defaultConfig(), &MockDB{}, &MockStorage{})

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()

	srv.handler404()(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

// ---------------------------------------------------------------------------
// indexHandler
// ---------------------------------------------------------------------------

func Test_indexHandler(t *testing.T) {
	srv := newTestServer(t, defaultConfig(), &MockDB{}, &MockStorage{})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.indexHandler()(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

// ---------------------------------------------------------------------------
// faviconHandler
// ---------------------------------------------------------------------------

func Test_faviconHandler(t *testing.T) {
	cfg := defaultConfig()
	cfg.FaviconFormat = "png"
	srv := newTestServer(t, cfg, &MockDB{}, &MockStorage{})

	faviconBytes := []byte{0x89, 0x50, 0x4E, 0x47} // PNG magic bytes

	req := httptest.NewRequest(http.MethodGet, "/favicon.png", nil)
	w := httptest.NewRecorder()

	srv.faviconHandler(faviconBytes).ServeHTTP(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	assert.NotEmpty(t, resp.Header.Get("cache-control"))

	body := w.Body.Bytes()
	assert.Equal(t, faviconBytes, body)
}

// ---------------------------------------------------------------------------
// staticFilesHandler
// ---------------------------------------------------------------------------

func Test_staticFilesHandler(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{
			name:           "existing static file",
			path:           "/static/upload.css",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "path traversal blocked",
			path:           "/static/../webserver.go",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, defaultConfig(), &MockDB{}, &MockStorage{})

			staticDir, _ := fs.Sub(staticFilesEmbedded, "static")
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			// gorilla/mux vars must be set manually in unit tests
			req = mux.SetURLVars(req, map[string]string{
				"file": strings.TrimPrefix(tt.path, "/static/"),
			})
			w := httptest.NewRecorder()

			srv.staticFilesHandler(staticDir).ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

// ---------------------------------------------------------------------------
// downloadHandler
// ---------------------------------------------------------------------------

func Test_downloadHandler(t *testing.T) {
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
			db := &MockDB{}
			stor := &MockStorage{}
			srv := newTestServer(t, defaultConfig(), db, stor)

			db.On("OnFileDownload", tt.fileID).Return(tt.downloadErr)
			db.On("GetFileMetadata", tt.fileID).Return(tt.metadata, tt.metadataErr)
			stor.On("ServeFile", mock.Anything, mock.Anything, tt.fileID, tt.fileName, tt.metadata, true, true).Return()

			path := "/d/" + tt.fileID
			vars := map[string]string{"fileID": tt.fileID}
			if tt.fileName != "" {
				path += "/" + tt.fileName
				vars["fileName"] = tt.fileName
			}

			req := httptest.NewRequest(http.MethodGet, path, nil)
			req = mux.SetURLVars(req, vars)
			w := httptest.NewRecorder()

			srv.downloadHandler().ServeHTTP(w, req)

			db.AssertExpectations(t)
			stor.AssertExpectations(t)
		})
	}
}

// ---------------------------------------------------------------------------
// uploadHandler
// ---------------------------------------------------------------------------

func Test_uploadHandler(t *testing.T) {
	// multipartBody builds a multipart/form-data request body containing a
	// single "file" field with the given filename and content.
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
		setup          func(t *testing.T) (*http.Request, *MockDB, *MockStorage)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "missing file field returns 400",
			setup: func(t *testing.T) (*http.Request, *MockDB, *MockStorage) {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return req, &MockDB{}, &MockStorage{}
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   "file' field is missing",
		},
		{
			name: "file too large returns 413",
			setup: func(t *testing.T) (*http.Request, *MockDB, *MockStorage) {
				cfg := defaultConfig()
				cfg.MaxFileSizeMB = 1
				// Build a header with a large declared size by crafting the request manually.
				body, ct := multipartBody(t, "big.bin", bytes.Repeat([]byte("x"), 10))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				// Override the parsed header size after parse by using a real parse:
				// The easiest way is to set MaxFileSizeMB very small so any file triggers it.
				// We set it to 0 on the config stored in the server, not the request.
				_ = cfg
				return req, &MockDB{}, &MockStorage{}
			},
			expectedStatus: http.StatusRequestEntityTooLarge,
			expectedBody:   "file size exceeds the maximum allowed limit",
		},
		{
			name: "SaveFileUpload error returns 500",
			setup: func(t *testing.T) (*http.Request, *MockDB, *MockStorage) {
				body, ct := multipartBody(t, "file.txt", []byte("hello"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := &MockDB{}
				stor := &MockStorage{}
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("", errors.New("storage failure"))
				return req, db, stor
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to process upload",
		},
		{
			name: "OnFileUpload error is non-fatal",
			setup: func(t *testing.T) (*http.Request, *MockDB, *MockStorage) {
				body, ct := multipartBody(t, "file.txt", []byte("hello"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := &MockDB{}
				stor := &MockStorage{}
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("/path/file.txt", nil)
				db.On("OnFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("db error"))
				return req, db, stor
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"status":"success"`,
		},
		{
			name: "happy path returns 200 with URL",
			setup: func(t *testing.T) (*http.Request, *MockDB, *MockStorage) {
				body, ct := multipartBody(t, "photo.png", []byte("image data"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := &MockDB{}
				stor := &MockStorage{}
				stor.On("SaveFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("/path/photo.png", nil)
				db.On("OnFileUpload", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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
				cfg.MaxFileSizeMB = 0 // any non-zero file will exceed 0 MB
			}

			req, db, stor := tt.setup(t)
			srv := newTestServer(t, cfg, db, stor)

			w := httptest.NewRecorder()
			srv.uploadHandler().ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), tt.expectedBody)

			db.AssertExpectations(t)
			stor.AssertExpectations(t)
		})
	}
}

// ---------------------------------------------------------------------------
// calculateExpirationTime
// ---------------------------------------------------------------------------

func Test_calculateExpirationTime(t *testing.T) {
	cfg := &models.Configuration{
		MaxFileSizeMB: 100,
		MinAgeDays:    30,
		MaxAgeDays:    365,
	}

	t.Run("no expires param uses formula", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		before := time.Now()
		exp := calculateExpirationTime(req, 0, cfg)
		after := time.Now()

		assert.True(t, exp.After(before))
		assert.True(t, exp.Before(after.Add(time.Duration(cfg.MaxAgeDays)*24*time.Hour+time.Second)))
	})

	t.Run("expires unix ms earlier than default is used", func(t *testing.T) {
		// value < 1000000 is treated as hours; use a large unix ms value
		target := time.Now().Add(1 * time.Hour)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/?expires=%d", target.UnixMilli()), nil)
		exp := calculateExpirationTime(req, 0, cfg)

		assert.WithinDuration(t, target, exp, 2*time.Second)
	})

	t.Run("expires unix ms later than default falls back to default", func(t *testing.T) {
		farFuture := time.Now().Add(10 * 365 * 24 * time.Hour)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/?expires=%d", farFuture.UnixMilli()), nil)
		exp := calculateExpirationTime(req, 0, cfg)

		assert.True(t, exp.Before(farFuture))
	})

	t.Run("invalid expires string falls back to default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/?expires=notanumber", nil)
		before := time.Now()
		exp := calculateExpirationTime(req, 0, cfg)

		assert.True(t, exp.After(before))
	})

	t.Run("expires in hours is interpreted as hours", func(t *testing.T) {
		// value < 1000000 is treated as hours
		req := httptest.NewRequest(http.MethodPost, "/?expires=2", nil)
		target := time.Now().Add(2 * time.Hour)
		exp := calculateExpirationTime(req, 0, cfg)

		assert.WithinDuration(t, target, exp, 2*time.Second)
	})
}

// ---------------------------------------------------------------------------
// fetchFavicon
// ---------------------------------------------------------------------------

func Test_fetchFavicon(t *testing.T) {
	t.Run("loads from local file without prefix", func(t *testing.T) {
		data := []byte{0x89, 0x50, 0x4E, 0x47}
		f, err := os.CreateTemp(t.TempDir(), "favicon*.png")
		require.NoError(t, err)
		f.Write(data)
		f.Close()

		ctx := context.Background()
		result, err := fetchFavicon(ctx, f.Name())
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("loads from file:// prefixed path", func(t *testing.T) {
		data := []byte{0x00, 0x01, 0x02}
		f, err := os.CreateTemp(t.TempDir(), "favicon*.ico")
		require.NoError(t, err)
		f.Write(data)
		f.Close()

		ctx := context.Background()
		result, err := fetchFavicon(ctx, "file://"+f.Name())
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("local file exceeding 4 KiB returns error", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "big*.png")
		require.NoError(t, err)
		f.Write(bytes.Repeat([]byte("x"), 4*1024+1))
		f.Close()

		ctx := context.Background()
		_, err = fetchFavicon(ctx, f.Name())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum allowed limit")
	})

	t.Run("http URL fetches and returns bytes", func(t *testing.T) {
		data := []byte{0x47, 0x49, 0x46} // GIF magic
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		}))
		defer ts.Close()

		ctx := context.Background()
		result, err := fetchFavicon(ctx, ts.URL+"/favicon.gif")
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("http URL non-200 response returns error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		ctx := context.Background()
		_, err := fetchFavicon(ctx, ts.URL+"/missing.png")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404")
	})

	t.Run("http URL exceeding 4 KiB Content-Length returns error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", 4*1024+1))
			w.WriteHeader(http.StatusOK)
			w.Write(bytes.Repeat([]byte("x"), 4*1024+1))
		}))
		defer ts.Close()

		ctx := context.Background()
		_, err := fetchFavicon(ctx, ts.URL+"/big.png")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum allowed limit")
	})
}
