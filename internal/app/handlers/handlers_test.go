package handlers_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/app/auth"
	"github.com/markhc/isrv/internal/app/handlers"
	"github.com/markhc/isrv/internal/database"
	dbmocks "github.com/markhc/isrv/internal/database/mocks"
	"github.com/markhc/isrv/internal/encryption"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	stmocks "github.com/markhc/isrv/internal/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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
	s.EXPECT().Backend().Return("local").Maybe()
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
// Download
// ---------------------------------------------------------------------------

func Test_Download(t *testing.T) {
	tests := []struct {
		name         string
		file         models.File
		resolvedName string
		downloadErr  error
	}{
		{
			name: "happy path without filename",
			file: models.File{
				ID:          "abc123",
				Name:        "",
				ContentType: "image/png",
			},
			resolvedName: "stored.png",
		},
		{
			name: "happy path with filename",
			file: models.File{
				ID:          "abc123",
				Name:        "photo.png",
				ContentType: "image/png",
			},
			resolvedName: "photo.png",
		},
		{
			name: "OnFileDownload error is non-fatal",
			file: models.File{
				ID:          "abc123",
				Name:        "",
				ContentType: "",
			},
			resolvedName: "stored.png",
			downloadErr:  errors.New("db error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			stor := newMockStorage(t)

			db.EXPECT().GetFile(mock.Anything, tt.file.ID).Return(&tt.file, nil)
			db.EXPECT().OnFileDownload(mock.Anything, tt.file.ID).Return(tt.downloadErr)
			stor.EXPECT().PresignedURL(mock.Anything, &tt.file).Return("", false, nil)
			stor.EXPECT().Open(mock.Anything, tt.file.ID, mock.Anything).Return(&storage.Object{
				Body:   io.NopCloser(strings.NewReader("file-bytes")),
				Size:   int64(len("file-bytes")),
				Length: int64(len("file-bytes")),
			}, nil)

			app := newApp()
			h := handlers.Download(db, stor, nil)
			app.Get("/d/:id", h)
			app.Get("/d/:id/:filename", h)

			path := "/d/" + tt.file.ID
			if tt.file.Name != "" {
				path += "/" + tt.file.Name
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
			name: "Save error returns 500",
			setup: func(t *testing.T) (*http.Request, *dbmocks.MockDatabase, *stmocks.MockStorage) {
				body, ct := multipartBody(t, "file.txt", []byte("hello"))
				req := httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set("Content-Type", ct)
				db := dbmocks.NewMockDatabase(t)
				stor := newMockStorage(t)
				stor.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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
				stor.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("/path/file.txt", nil)
				db.EXPECT().OnFileUpload(mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("db error"))
				stor.EXPECT().DeleteFile(mock.Anything, mock.Anything).Return(nil)
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
				stor.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return("/path/photo.png", nil)
				db.EXPECT().OnFileUpload(mock.Anything, mock.Anything, mock.Anything).
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
			app.Post("/", handlers.Upload(cfg, db, stor, nil))

			resp := doTest(t, app, req)
			body := bodyString(t, resp)

			assert.Equal(t, tt.expectedStatus, resp.StatusCode)
			assert.Contains(t, body, tt.expectedBody)
		})
	}
}

// ---------------------------------------------------------------------------
// Encryption
// ---------------------------------------------------------------------------

// newEncManager builds an encryption.Manager from a freshly generated identity.
func newEncManager(t *testing.T) *encryption.Manager {
	t.Helper()

	id, err := encryption.GenerateIdentity()
	require.NoError(t, err)

	m, err := encryption.NewManager(models.EncryptionConfiguration{Identity: id})
	require.NoError(t, err)
	require.NotNil(t, m)

	return m
}

// encMultipartBody builds a multipart body carrying a single "file" field.
func encMultipartBody(t *testing.T, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, _ = fw.Write(content)
	require.NoError(t, mw.Close())

	return &buf, mw.FormDataContentType()
}

func Test_Upload_Encryption(t *testing.T) {
	enc := newEncManager(t)

	plaintext := []byte("secret upload contents that should be encrypted at rest")

	body, ct := encMultipartBody(t, "secret.txt", plaintext)
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", ct)

	db := dbmocks.NewMockDatabase(t)
	stor := newMockStorage(t)

	var stored []byte
	stor.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(opts storage.SaveOptions) bool {
		return opts.Size == -1 && opts.ContentType == "application/octet-stream"
	})).Run(func(_ context.Context, _ string, r io.Reader, _ storage.SaveOptions) {
		stored, _ = io.ReadAll(r)
	}).Return("/path/secret.txt", nil)

	db.EXPECT().OnFileUpload(mock.Anything, mock.MatchedBy(func(f *models.File) bool {
		return f.EncryptionVersion == encryption.VersionAgeV1 && f.Size == int64(len(plaintext))
	}), mock.Anything).Return(nil)

	cfg := defaultConfig()
	cfg.Encryption.Enabled = true

	app := newApp()
	app.Post("/", handlers.Upload(cfg, db, stor, enc))

	resp := doTest(t, app, req)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	require.NotEmpty(t, stored)
	assert.NotEqual(t, plaintext, stored, "stored bytes must be ciphertext")
	assert.True(t, bytes.HasPrefix(stored, []byte("age-encryption.org/v1")),
		"stored object must start with the age magic")

	// The ciphertext must decrypt back to the original plaintext.
	dec, err := enc.Decrypt(bytes.NewReader(stored))
	require.NoError(t, err)
	got, err := io.ReadAll(dec)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

func Test_Download_Encryption(t *testing.T) {
	enc := newEncManager(t)

	plaintext := []byte("decrypted download contents")

	// Produce the stored ciphertext as the upload path would.
	encReader, err := enc.Encrypt(bytes.NewReader(plaintext))
	require.NoError(t, err)
	ciphertext, err := io.ReadAll(encReader)
	require.NoError(t, err)

	newEncryptedFile := func() *models.File {
		return &models.File{
			ID:                "enc-file",
			Name:              "secret.txt",
			ContentType:       "text/plain",
			Size:              int64(len(plaintext)),
			EncryptionVersion: encryption.VersionAgeV1,
		}
	}

	openCiphertext := func(stor *stmocks.MockStorage) {
		stor.EXPECT().Open(mock.Anything, "enc-file", (*storage.ByteRange)(nil)).Return(&storage.Object{
			Body:   io.NopCloser(bytes.NewReader(ciphertext)),
			Size:   int64(len(ciphertext)),
			Length: int64(len(ciphertext)),
		}, nil)
	}

	t.Run("round-trip returns plaintext with DB metadata", func(t *testing.T) {
		file := newEncryptedFile()
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)

		db.EXPECT().GetFile(mock.Anything, file.ID).Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, file.ID).Return(nil)
		openCiphertext(stor)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, enc))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/enc-file", nil))
		got := bodyString(t, resp)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, string(plaintext), got)
		assert.Equal(t, strconv.Itoa(len(plaintext)), resp.Header.Get("Content-Length"))
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

		// PresignedURL must never be consulted for encrypted files.
		stor.AssertNotCalled(t, "PresignedURL", mock.Anything, mock.Anything)
	})

	t.Run("range header is ignored: full body, no accept-ranges", func(t *testing.T) {
		file := newEncryptedFile()
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)

		db.EXPECT().GetFile(mock.Anything, file.ID).Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, file.ID).Return(nil)
		openCiphertext(stor)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, enc))

		r := httptest.NewRequest(http.MethodGet, "/d/enc-file", nil)
		r.Header.Set("Range", "bytes=0-3")
		resp := doTest(t, app, r)
		got := bodyString(t, resp)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, string(plaintext), got)
		assert.Empty(t, resp.Header.Get("Accept-Ranges"))
		assert.Empty(t, resp.Header.Get("Content-Range"))
	})

	t.Run("nil manager returns 500 without leaking bytes", func(t *testing.T) {
		file := newEncryptedFile()
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)

		db.EXPECT().GetFile(mock.Anything, file.ID).Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, file.ID).Return(nil)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/enc-file", nil))
		got := bodyString(t, resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.NotContains(t, got, string(plaintext))
		stor.AssertNotCalled(t, "Open", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("wrong identity returns 500 without leaking bytes", func(t *testing.T) {
		file := newEncryptedFile()
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)

		db.EXPECT().GetFile(mock.Anything, file.ID).Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, file.ID).Return(nil)
		openCiphertext(stor)

		wrongManager := newEncManager(t) // different identity

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, wrongManager))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/enc-file", nil))
		got := bodyString(t, resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.NotContains(t, got, string(plaintext))
	})
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
				st.EXPECT().DeleteFile(mock.Anything, "abc123").Return(nil)
				db.EXPECT().OnFileDelete(mock.Anything, "abc123").Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:   "storage error returns 500",
			fileID: "abc123",
			setup: func(db *dbmocks.MockDatabase, st *stmocks.MockStorage) {
				st.EXPECT().DeleteFile(mock.Anything, "abc123").Return(errors.New("disk full"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "failed to delete file",
		},
		{
			name:   "database error after storage deletion returns 500",
			fileID: "abc123",
			setup: func(db *dbmocks.MockDatabase, st *stmocks.MockStorage) {
				st.EXPECT().DeleteFile(mock.Anything, "abc123").Return(nil)
				db.EXPECT().OnFileDelete(mock.Anything, "abc123").Return(errors.New("db error"))
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
				db.EXPECT().GetFile(mock.Anything, "abc123").Return(smallFileRecord(), nil)
				db.EXPECT().SetExpiration(mock.Anything, "abc123", mock.AnythingOfType("time.Time")).Return(nil)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   `"fileId":"abc123"`,
		},
		{
			name:   "happy path — hours shorthand within limit",
			fileID: "abc123",
			body:   expiresJSON("48"),
			setup: func(db *dbmocks.MockDatabase) {
				db.EXPECT().GetFile(mock.Anything, "abc123").Return(smallFileRecord(), nil)
				db.EXPECT().SetExpiration(mock.Anything, "abc123", mock.AnythingOfType("time.Time")).Return(nil)
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
				db.EXPECT().GetFile(mock.Anything, "abc123").Return(smallFileRecord(), nil)
			},
			expectedStatus: http.StatusUnprocessableEntity,
			expectedBody:   "expiration exceeds the maximum allowed time",
		},
		{
			name:   "file not found returns 404",
			fileID: "missing",
			body:   expiresJSON("24"),
			setup: func(db *dbmocks.MockDatabase) {
				db.EXPECT().GetFile(mock.Anything, "missing").Return(nil, database.ErrFileNotFound)
			},
			expectedStatus: http.StatusNotFound,
			expectedBody:   "file not found",
		},
		{
			name:   "database error on GetFile returns 500",
			fileID: "abc123",
			body:   expiresJSON("24"),
			setup: func(db *dbmocks.MockDatabase) {
				db.EXPECT().GetFile(mock.Anything, "abc123").Return(nil, database.ErrDatabase)
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "internal server error",
		},
		{
			name:   "database error on SetExpiration returns 500",
			fileID: "abc123",
			body:   expiresJSON(unixMsIn(24 * time.Hour)),
			setup: func(db *dbmocks.MockDatabase) {
				db.EXPECT().GetFile(mock.Anything, "abc123").Return(smallFileRecord(), nil)
				db.EXPECT().SetExpiration(mock.Anything, "abc123", mock.AnythingOfType("time.Time")).Return(errors.New("db error"))
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

// ---------------------------------------------------------------------------
// Health
// ---------------------------------------------------------------------------

func Test_Healthz(t *testing.T) {
	app := newApp()
	app.Get("/healthz", handlers.Healthz())

	resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	body := bodyString(t, resp)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")
	assert.Contains(t, body, `"status":"ok"`)
}

func Test_Readyz(t *testing.T) {
	tests := []struct {
		name       string
		dbErr      error
		storErr    error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "all healthy returns 200",
			wantStatus: http.StatusOK,
			wantBody:   `"status":"ok"`,
		},
		{
			name:       "database down returns 503",
			dbErr:      errors.New("db unreachable"),
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "db unreachable",
		},
		{
			name:       "storage down returns 503",
			storErr:    errors.New("bucket unreachable"),
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "bucket unreachable",
		},
		{
			name:       "both down returns 503",
			dbErr:      errors.New("db unreachable"),
			storErr:    errors.New("bucket unreachable"),
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `"status":"unavailable"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			stor := newMockStorage(t)
			db.EXPECT().Ping(mock.Anything).Return(tt.dbErr)
			stor.EXPECT().HealthCheck(mock.Anything).Return(tt.storErr)

			app := newApp()
			app.Get("/readyz", handlers.Readyz(db, stor))

			resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			body := bodyString(t, resp)

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Contains(t, body, tt.wantBody)
		})
	}
}

// ---------------------------------------------------------------------------
// Static (success + not-found)
// ---------------------------------------------------------------------------

func Test_Static_Serve(t *testing.T) {
	staticDir := fstest.MapFS{
		"assets/app.js": {Data: []byte("console.log('hi')")},
	}

	app := newApp()
	app.Get("/static/*", handlers.Static(staticDir))

	t.Run("serves existing file", func(t *testing.T) {
		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/static/assets/app.js", nil))
		body := bodyString(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "console.log('hi')", body)
		assert.NotEmpty(t, resp.Header.Get("Cache-Control"))
		assert.Contains(t, resp.Header.Get("Content-Type"), "javascript")
	})

	t.Run("missing file returns 404", func(t *testing.T) {
		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/static/assets/missing.js", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

// ---------------------------------------------------------------------------
// SPA
// ---------------------------------------------------------------------------

func Test_SPA(t *testing.T) {
	distFS := fstest.MapFS{
		"index.html":    {Data: []byte("<html><head></head><body>app</body></html>")},
		"assets/app.js": {Data: []byte("console.log('spa')")},
	}
	cfg := models.FrontendConfig{}

	app := newApp()
	app.Use("/*", handlers.SPA(distFS, cfg))

	t.Run("root serves index with injected config", func(t *testing.T) {
		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/", nil))
		body := bodyString(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
		assert.Contains(t, body, "window.__ISRV_CONFIG__")
	})

	t.Run("serves existing asset", func(t *testing.T) {
		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
		body := bodyString(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "console.log('spa')", body)
		assert.NotEmpty(t, resp.Header.Get("Cache-Control"))
	})

	t.Run("unknown path falls back to index", func(t *testing.T) {
		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/some/client/route", nil))
		body := bodyString(t, resp)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, body, "app")
	})

	t.Run("path traversal blocked", func(t *testing.T) {
		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/%2e%2e/secret", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func Test_SPA_FrontendNotBuilt(t *testing.T) {
	// No index.html present -> placeholder handler.
	distFS := fstest.MapFS{}

	app := newApp()
	app.Use("/*", handlers.SPA(distFS, models.FrontendConfig{}))

	resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/", nil))
	body := bodyString(t, resp)

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Contains(t, body, "frontend not built")
}

// ---------------------------------------------------------------------------
// AdminLogout
// ---------------------------------------------------------------------------

func Test_AdminLogout_ClearsCookie(t *testing.T) {
	app := newApp()
	app.Post("/admin/logout", handlers.AdminLogout())

	resp := doTest(t, app, httptest.NewRequest(http.MethodPost, "/admin/logout", nil))
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	require.NotNil(t, cookie, "logout must emit the session cookie")
	assert.Empty(t, cookie.Value, "logout must clear the cookie value")
	// The cookie is expired: either an explicit past Expires or MaxAge<=0.
	expired := (!cookie.Expires.IsZero() && cookie.Expires.Before(time.Now())) || cookie.MaxAge < 0
	assert.True(t, expired, "logout must expire the cookie")
}

// ---------------------------------------------------------------------------
// Admin list/delete error branches
// ---------------------------------------------------------------------------

func Test_AdminListFiles_DBError(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	db.EXPECT().ListFiles(mock.Anything, mock.Anything).Return(nil, 0, errors.New("db down"))

	app := newApp()
	app.Get("/files", handlers.AdminListFiles(db))

	resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/files", nil))
	body := bodyString(t, resp)

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Contains(t, body, "failed to list files")
}

func Test_AdminDeleteFile_Branches(t *testing.T) {
	t.Run("lookup error returns 500", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		st := newMockStorage(t)
		db.EXPECT().GetFile(mock.Anything, "id").Return(nil, errors.New("db down"))

		app := newApp()
		app.Delete("/files/:id", handlers.AdminDeleteFile(db, st))

		resp := doTest(t, app, httptest.NewRequest(http.MethodDelete, "/files/id", nil))
		body := bodyString(t, resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Contains(t, body, "internal server error")
	})

	t.Run("delete failure returns 500", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		st := newMockStorage(t)
		db.EXPECT().GetFile(mock.Anything, "id").Return(&models.File{ID: "id"}, nil)
		st.EXPECT().DeleteFile(mock.Anything, "id").Return(errors.New("disk full"))

		app := newApp()
		app.Delete("/files/:id", handlers.AdminDeleteFile(db, st))

		resp := doTest(t, app, httptest.NewRequest(http.MethodDelete, "/files/id", nil))
		body := bodyString(t, resp)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Contains(t, body, "failed to delete file")
	})
}

// ---------------------------------------------------------------------------
// Download edge branches (serveStream / serveEncrypted / Download)
// ---------------------------------------------------------------------------

func Test_Download_EdgeCases(t *testing.T) {
	t.Run("file not found returns 404", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		db.EXPECT().GetFile(mock.Anything, "missing").Return(nil, database.ErrFileNotFound)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/missing", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("database error returns 500", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		db.EXPECT().GetFile(mock.Anything, "id").Return(nil, database.ErrDatabase)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/id", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("presigned URL redirects", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		file := &models.File{ID: "id", ContentType: "image/png"}
		db.EXPECT().GetFile(mock.Anything, "id").Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, "id").Return(nil)
		stor.EXPECT().PresignedURL(mock.Anything, file).Return("https://cdn.example/file", true, nil)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/id", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusFound, resp.StatusCode)
		assert.Equal(t, "https://cdn.example/file", resp.Header.Get("Location"))
	})

	t.Run("presigned URL error returns 500", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		file := &models.File{ID: "id"}
		db.EXPECT().GetFile(mock.Anything, "id").Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, "id").Return(nil)
		stor.EXPECT().PresignedURL(mock.Anything, file).Return("", false, errors.New("sign failed"))

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/id", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("stream object not found returns 404", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		file := &models.File{ID: "id"}
		db.EXPECT().GetFile(mock.Anything, "id").Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, "id").Return(nil)
		stor.EXPECT().PresignedURL(mock.Anything, file).Return("", false, nil)
		stor.EXPECT().Open(mock.Anything, "id", mock.Anything).Return(nil, storage.ErrObjectNotFound)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/id", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("stream invalid range returns 416", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		file := &models.File{ID: "id"}
		db.EXPECT().GetFile(mock.Anything, "id").Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, "id").Return(nil)
		stor.EXPECT().PresignedURL(mock.Anything, file).Return("", false, nil)
		stor.EXPECT().Open(mock.Anything, "id", mock.MatchedBy(func(r *storage.ByteRange) bool {
			return r != nil && r.Start == 100 && r.End == 200
		})).Return(nil, storage.ErrInvalidRange)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		r := httptest.NewRequest(http.MethodGet, "/d/id", nil)
		r.Header.Set("Range", "bytes=100-200")
		resp := doTest(t, app, r)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, resp.StatusCode)
	})

	t.Run("stream open error returns 500", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		file := &models.File{ID: "id"}
		db.EXPECT().GetFile(mock.Anything, "id").Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, "id").Return(nil)
		stor.EXPECT().PresignedURL(mock.Anything, file).Return("", false, nil)
		stor.EXPECT().Open(mock.Anything, "id", mock.Anything).Return(nil, errors.New("io error"))

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/id", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})

	t.Run("partial content returns 206 and uses object content-type", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		// Empty file.ContentType exercises the obj.ContentType fallback.
		file := &models.File{ID: "id", ContentType: ""}
		db.EXPECT().GetFile(mock.Anything, "id").Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, "id").Return(nil)
		stor.EXPECT().PresignedURL(mock.Anything, file).Return("", false, nil)
		stor.EXPECT().Open(mock.Anything, "id", mock.Anything).Return(&storage.Object{
			Body:         io.NopCloser(bytes.NewReader([]byte("PART"))),
			Size:         10,
			Length:       4,
			ContentType:  "text/plain",
			Partial:      true,
			ContentRange: "bytes 0-3/10",
		}, nil)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, nil))

		r := httptest.NewRequest(http.MethodGet, "/d/id", nil)
		r.Header.Set("Range", "bytes=0-3")
		resp := doTest(t, app, r)
		body := bodyString(t, resp)

		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Equal(t, "PART", body)
		assert.Equal(t, "bytes 0-3/10", resp.Header.Get("Content-Range"))
		assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/plain")
	})
}

func Test_Download_Encrypted_EdgeCases(t *testing.T) {
	enc := newEncManager(t)

	encFile := func() *models.File {
		return &models.File{
			ID:                "enc",
			Name:              "s.txt",
			Size:              10,
			EncryptionVersion: encryption.VersionAgeV1,
		}
	}

	t.Run("encrypted object not found returns 404", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		file := encFile()
		db.EXPECT().GetFile(mock.Anything, "enc").Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, "enc").Return(nil)
		stor.EXPECT().Open(mock.Anything, "enc", (*storage.ByteRange)(nil)).Return(nil, storage.ErrObjectNotFound)

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, enc))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/enc", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("encrypted open error returns 500", func(t *testing.T) {
		db := dbmocks.NewMockDatabase(t)
		stor := newMockStorage(t)
		file := encFile()
		db.EXPECT().GetFile(mock.Anything, "enc").Return(file, nil)
		db.EXPECT().OnFileDownload(mock.Anything, "enc").Return(nil)
		stor.EXPECT().Open(mock.Anything, "enc", (*storage.ByteRange)(nil)).Return(nil, errors.New("io error"))

		app := newApp()
		app.Get("/d/:id", handlers.Download(db, stor, enc))

		resp := doTest(t, app, httptest.NewRequest(http.MethodGet, "/d/enc", nil))
		defer resp.Body.Close()
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}
