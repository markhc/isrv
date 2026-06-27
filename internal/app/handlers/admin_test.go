package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/app/auth"
	"github.com/markhc/isrv/internal/database"
	dbmocks "github.com/markhc/isrv/internal/database/mocks"
	"github.com/markhc/isrv/internal/models"
	stormocks "github.com/markhc/isrv/internal/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func testAdminConfig() models.AdminConfiguration {
	return models.AdminConfiguration{
		Username:      "admin",
		Password:      "secret",
		SessionSecret: "session-secret",
		SessionTTL:    time.Hour,
	}
}

func TestAdminLogin_ValidCredentials_SetsCookie(t *testing.T) {
	cfg := testAdminConfig()
	app := fiber.New()
	app.Post("/login", AdminLogin(cfg))

	body := strings.NewReader(`{"username":"admin","password":"secret"}`)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	require.NotNil(t, cookie, "session cookie must be set")
	_, ok := auth.Validate([]byte(cfg.SessionSecret), cookie.Value)
	assert.True(t, ok)
}

func TestAdminLogin_InvalidCredentials_Unauthorized(t *testing.T) {
	cfg := testAdminConfig()
	app := fiber.New()
	app.Post("/login", AdminLogin(cfg))

	body := strings.NewReader(`{"username":"admin","password":"wrong"}`)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAdminSession_ValidCookie_ReportsAuthenticated(t *testing.T) {
	cfg := testAdminConfig()
	app := fiber.New()
	app.Get("/session", AdminSession(cfg))

	token := auth.IssueToken([]byte(cfg.SessionSecret), cfg.Username, cfg.SessionTTL)
	req := httptest.NewRequest(http.MethodGet, "/session", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var out sessionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.True(t, out.Authenticated)
	assert.Equal(t, "admin", out.Username)
}

func TestAdminSession_NoCookie_ReportsUnauthenticated(t *testing.T) {
	cfg := testAdminConfig()
	app := fiber.New()
	app.Get("/session", AdminSession(cfg))

	req := httptest.NewRequest(http.MethodGet, "/session", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	var out sessionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.False(t, out.Authenticated)
}

func TestAdminListFiles_ReturnsPaginatedResults(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	db.EXPECT().
		ListFiles(mock.Anything, mock.MatchedBy(func(f models.FileListFilter) bool {
			return f.IP == "10.0.0.1" && f.Limit == 50
		})).
		Return([]models.File{{ID: "a", Name: "a.txt"}}, 1, nil)

	app := fiber.New()
	app.Get("/files", AdminListFiles(db))

	req := httptest.NewRequest(http.MethodGet, "/files?ip=10.0.0.1", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var out listFilesResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Equal(t, 1, out.Total)
	assert.Equal(t, 50, out.Limit)
	require.Len(t, out.Items, 1)
	assert.Equal(t, "a", out.Items[0].ID)
}

func TestAdminListFiles_ClampsLimit(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	db.EXPECT().
		ListFiles(mock.Anything, mock.MatchedBy(func(f models.FileListFilter) bool {
			return f.Limit == maxAdminPageSize
		})).
		Return([]models.File{}, 0, nil)

	app := fiber.New()
	app.Get("/files", AdminListFiles(db))

	req := httptest.NewRequest(http.MethodGet, "/files?limit=9999", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAdminDeleteFile_Success(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	st := stormocks.NewMockStorage(t)

	db.EXPECT().GetFile(mock.Anything, "file-id").Return(&models.File{ID: "file-id"}, nil)
	st.EXPECT().DeleteFile(mock.Anything, "file-id").Return(nil)
	db.EXPECT().OnFileDelete(mock.Anything, "file-id").Return(nil)

	app := fiber.New()
	app.Delete("/files/:id", AdminDeleteFile(db, st))

	req := httptest.NewRequest(http.MethodDelete, "/files/file-id", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestAdminDeleteFile_NotFound(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	st := stormocks.NewMockStorage(t)

	db.EXPECT().GetFile(mock.Anything, "missing").Return(nil, database.ErrFileNotFound)

	app := fiber.New()
	app.Delete("/files/:id", AdminDeleteFile(db, st))

	req := httptest.NewRequest(http.MethodDelete, "/files/missing", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, string(body), "file not found")
}
