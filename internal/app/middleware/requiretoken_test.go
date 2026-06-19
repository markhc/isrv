package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/database/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newTokenApp creates a Fiber app with RequireToken middleware on a /:id route.
func newTokenApp(db database.Database) *fiber.App {
	app := fiber.New()
	app.Get("/:id", RequireToken(db), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	return app
}

func TestRequireToken_NoToken_ReturnsUnauthorized(t *testing.T) {
	app := newTokenApp(mocks.NewMockDatabase(t))

	req := httptest.NewRequest(http.MethodGet, "/file-id", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRequireToken_ValidToken_QueryParam_Allows(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "secret").Return("file-id", nil)

	app := newTokenApp(db)

	req := httptest.NewRequest(http.MethodGet, "/file-id?token=secret", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRequireToken_ValidToken_BearerHeader_Allows(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "secret").Return("file-id", nil)

	app := newTokenApp(db)

	req := httptest.NewRequest(http.MethodGet, "/file-id", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestRequireToken_InvalidToken_ReturnsUnauthorized(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "badtoken").Return("", database.ErrFileNotFound)

	app := newTokenApp(db)

	req := httptest.NewRequest(http.MethodGet, "/file-id?token=badtoken", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestRequireToken_TokenFileIDMismatch_ReturnsUnauthorized(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "token-for-other-file").Return("other-file-id", nil)

	app := newTokenApp(db)

	req := httptest.NewRequest(http.MethodGet, "/requested-file-id?token=token-for-other-file", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, string(body), "invalid token")
}

func TestRequireToken_DatabaseError_ReturnsInternalServerError(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "sometoken").Return("", database.ErrDatabase)

	app := newTokenApp(db)

	req := httptest.NewRequest(http.MethodGet, "/file-id?token=sometoken", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestRequireToken_MalformedAuthorizationHeader_ReturnsUnauthorized(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"no bearer prefix", "secret"},
		{"basic auth", "Basic dXNlcjpwYXNz"},
		{"bearer prefix only", "Bearer "},
		{"bearer lowercase", "bearer secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newTokenApp(mocks.NewMockDatabase(t))

			req := httptest.NewRequest(http.MethodGet, "/file-id", nil)
			req.Header.Set("Authorization", tt.header)
			resp, err := app.Test(req)
			require.NoError(t, err)
			resp.Body.Close()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}
}

func TestRequireToken_QueryParamTakesPrecedenceOverHeader(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "fromquery").Return("file-id", nil)

	app := newTokenApp(db)

	req := httptest.NewRequest(http.MethodGet, "/file-id?token=fromquery", nil)
	req.Header.Set("Authorization", "Bearer fromheader")
	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
