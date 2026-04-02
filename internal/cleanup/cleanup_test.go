package cleanup

import (
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/markhc/isrv/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

// MockDB implements database.Database using testify/mock.
type MockDB struct{ mock.Mock }

func (m *MockDB) Connect() error { return m.Called().Error(0) }
func (m *MockDB) Close() error   { return m.Called().Error(0) }
func (m *MockDB) Migrate() error { return m.Called().Error(0) }

func (m *MockDB) OnFileUpload(fileID string, fileHeader *multipart.FileHeader, expirationTime time.Time, ipAddress string) error {
	return m.Called(fileID, fileHeader, expirationTime, ipAddress).Error(0)
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

func Test_Service_performCleanup_expiredFiles(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}
	expectedFiles := []string{"file1", "file2", "file3"}

	db.On("GetExpiredFiles").Return(expectedFiles, nil)
	for _, f := range expectedFiles {
		stor.On("DeleteFile", mock.Anything, f).Return(nil)
		db.On("OnFileDelete", f).Return(nil)
	}

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())

	db.AssertExpectations(t)
	stor.AssertExpectations(t)
}

func Test_Service_performCleanup_noExpiredFiles(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	db.On("GetExpiredFiles").Return([]string{}, nil)

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())

	db.AssertExpectations(t)
	stor.AssertNotCalled(t, "DeleteFile")
	db.AssertNotCalled(t, "OnFileDelete")
}

func Test_Service_performCleanup_dbError(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	db.On("GetExpiredFiles").Return(nil, errors.New("database error"))

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())

	db.AssertExpectations(t)
	stor.AssertNotCalled(t, "DeleteFile")
	db.AssertNotCalled(t, "OnFileDelete")
}

func Test_Service_performCleanup_storageError(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	db.On("GetExpiredFiles").Return([]string{"file1"}, nil)
	stor.On("DeleteFile", mock.Anything, "file1").Return(errors.New("storage error"))
	db.On("OnFileDelete", "file1").Return(nil)

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())

	db.AssertExpectations(t)
	stor.AssertExpectations(t)
}

func Test_Service_performCleanup_databaseDeleteError(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	db.On("GetExpiredFiles").Return([]string{"file1"}, nil)
	stor.On("DeleteFile", mock.Anything, "file1").Return(nil)
	db.On("OnFileDelete", "file1").Return(errors.New("database delete error"))

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())

	db.AssertExpectations(t)
	stor.AssertExpectations(t)
}

func Test_Service_Start_disabled(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	service := NewService(db, stor, false, time.Minute) // enabled=false

	cancel := service.Start(context.Background())
	if cancel != nil {
		cancel()
	}
	service.Join() // should not panic or block

	db.AssertNotCalled(t, "GetExpiredFiles")
}

func Test_Service_Start_enabled(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	// GetExpiredFiles may or may not be called depending on timing.
	db.On("GetExpiredFiles").Return([]string{}, nil).Maybe()

	service := NewService(db, stor, true, time.Millisecond*10)

	cancel := service.Start(context.Background())

	time.Sleep(time.Millisecond * 5)

	if cancel != nil {
		cancel()
	}
	service.Join()

	db.AssertExpectations(t)
}

func Test_Service_cleanupFile_success(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	stor.On("DeleteFile", mock.Anything, "test-file").Return(nil)
	db.On("OnFileDelete", "test-file").Return(nil)

	service := NewService(db, stor, true, time.Minute)

	err := service.cleanupFile(context.Background(), "test-file")

	require.NoError(t, err)
	db.AssertExpectations(t)
	stor.AssertExpectations(t)
}

func Test_Service_cleanupFile_storageErrorOnly(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	stor.On("DeleteFile", mock.Anything, "test-file").Return(errors.New("storage failed"))
	db.On("OnFileDelete", "test-file").Return(nil)

	service := NewService(db, stor, true, time.Minute)

	err := service.cleanupFile(context.Background(), "test-file")

	require.Error(t, err)
	db.AssertExpectations(t)
	stor.AssertExpectations(t)
}

func Test_Service_cleanupFile_databaseErrorOnly(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	stor.On("DeleteFile", mock.Anything, "test-file").Return(nil)
	db.On("OnFileDelete", "test-file").Return(errors.New("database failed"))

	service := NewService(db, stor, true, time.Minute)

	err := service.cleanupFile(context.Background(), "test-file")

	require.Error(t, err)
	db.AssertExpectations(t)
	stor.AssertExpectations(t)
}

func Test_Service_cleanupFile_bothErrors(t *testing.T) {
	db := &MockDB{}
	stor := &MockStorage{}

	stor.On("DeleteFile", mock.Anything, "test-file").Return(errors.New("storage failed"))
	db.On("OnFileDelete", "test-file").Return(errors.New("database failed"))

	service := NewService(db, stor, true, time.Minute)

	err := service.cleanupFile(context.Background(), "test-file")

	// Storage error is returned as the primary error when both fail.
	assert.EqualError(t, err, "failed to delete file from storage: storage failed")
	db.AssertExpectations(t)
	stor.AssertExpectations(t)
}
