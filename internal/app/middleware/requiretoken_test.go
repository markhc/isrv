package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/database/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// withFileID injects a chi route context carrying the given file ID.
func withFileID(r *http.Request, fileID string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fileID)

	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestRequireToken_NoToken_ReturnsUnauthorized(t *testing.T) {
	handler := RequireToken(mocks.NewMockDatabase(t))(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireToken_ValidToken_QueryParam_Allows(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "secret").Return("file-id", nil)

	handler := RequireToken(db)(okHandler())

	req := withFileID(httptest.NewRequest(http.MethodGet, "/?token=secret", nil), "file-id")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireToken_ValidToken_BearerHeader_Allows(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "secret").Return("file-id", nil)

	handler := RequireToken(db)(okHandler())

	req := withFileID(httptest.NewRequest(http.MethodGet, "/", nil), "file-id")
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRequireToken_InvalidToken_ReturnsUnauthorized(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "badtoken").Return("", database.ErrFileNotFound)

	handler := RequireToken(db)(okHandler())

	req := httptest.NewRequest(http.MethodGet, "/?token=badtoken", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRequireToken_TokenFileIDMismatch_ReturnsUnauthorized(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "token-for-other-file").Return("other-file-id", nil)

	handler := RequireToken(db)(okHandler())

	req := withFileID(httptest.NewRequest(http.MethodGet, "/?token=token-for-other-file", nil), "requested-file-id")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid token")
}

func TestRequireToken_DatabaseError_ReturnsInternalServerError(t *testing.T) {
	db := mocks.NewMockDatabase(t)
	db.EXPECT().GetFileByToken(mock.Anything, "sometoken").Return("", database.ErrDatabase)

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
	db.EXPECT().GetFileByToken(mock.Anything, "fromquery").Return("file-id", nil)

	handler := RequireToken(db)(okHandler())

	req := withFileID(httptest.NewRequest(http.MethodGet, "/?token=fromquery", nil), "file-id")
	req.Header.Set("Authorization", "Bearer fromheader")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
