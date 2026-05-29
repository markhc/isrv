//nolint:all
package mocks

import (
	"context"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/stretchr/testify/mock"
)

// MockDB implements database.Database using testify/mock.
type MockDB struct{ mock.Mock }

func (m *MockDB) Connect() error { return m.Called().Error(0) }
func (m *MockDB) Close() error   { return m.Called().Error(0) }
func (m *MockDB) Migrate() error { return m.Called().Error(0) }

func (m *MockDB) OnFileUpload(fileID string, fileHeader *multipart.FileHeader, token string, expirationTime time.Time, ipAddress string) error {
	return m.Called(fileID, fileHeader, token, expirationTime, ipAddress).Error(0)
}

func (m *MockDB) OnFileDownload(fileID string) error {
	return m.Called(fileID).Error(0)
}

func (m *MockDB) OnFileDelete(fileID string) error {
	return m.Called(fileID).Error(0)
}

func (m *MockDB) GetFileMetadata(fileID string) (map[string]string, error) {
	args := m.Called(fileID)
	metadata, _ := args.Get(0).(map[string]string)
	return metadata, args.Error(1)
}

func (m *MockDB) GetFileToken(fileID string) (string, error) {
	args := m.Called(fileID)
	token, _ := args.Get(0).(string)
	return token, args.Error(1)
}

func (m *MockDB) GetExpiredFiles() ([]string, error) {
	args := m.Called()
	files, _ := args.Get(0).([]string)
	return files, args.Error(1)
}

// MockStorage implements storage.Storage using testify/mock.
type MockStorage struct{ mock.Mock }

func (m *MockStorage) FileExists(ctx context.Context, fileID string) (bool, error) {
	args := m.Called(ctx, fileID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStorage) SaveFileUpload(ctx context.Context, fileID string, file multipart.File, fileHeader *multipart.FileHeader) (string, error) {
	args := m.Called(ctx, fileID, file, fileHeader)
	return args.String(0), args.Error(1)
}

func (m *MockStorage) DeleteFile(ctx context.Context, fileID string) error {
	return m.Called(ctx, fileID).Error(0)
}

func (m *MockStorage) ServeFile(w http.ResponseWriter, r *http.Request, fileID string, fileName string, metadata map[string]string, inlineContent bool, cachingEnabled bool) {
	m.Called(w, r, fileID, fileName, metadata, inlineContent, cachingEnabled)
}
