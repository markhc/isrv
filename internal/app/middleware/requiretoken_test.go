package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/database/mocks"
	"github.com/stretchr/testify/assert"
)

func TestRequireToken_NoToken_ReturnsUnauthorized(t *testing.T) {
	handler := RequireToken(mocks.NewMockDatabase(t))(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireToken_ValidToken_QueryParam_Allows(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken("secret").Return("file-id", nil)

	handler := RequireToken(db)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/?token=secret", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireToken_ValidToken_BearerHeader_Allows(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken("secret").Return("file-id", nil)

	handler := RequireToken(db)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireToken_InvalidToken_ReturnsUnauthorized(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken("badtoken").Return("", database.ErrFileNotFound)

	handler := RequireToken(db)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/?token=badtoken", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireToken_DatabaseError_ReturnsInternalServerError(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken("sometoken").Return("", database.ErrDatabase)

	handler := RequireToken(db)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/?token=sometoken", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
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
			handler := RequireToken(mocks.NewMockDatabase(t))(okHandler())

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", tt.header)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}

func TestRequireToken_QueryParamTakesPrecedenceOverHeader(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken("fromquery").Return("file-id", nil)

	handler := RequireToken(db)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/?token=fromquery", nil)
	req.Header.Set("Authorization", "Bearer fromheader")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
