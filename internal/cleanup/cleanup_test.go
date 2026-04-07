package cleanup

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	dbmocks "github.com/markhc/isrv/internal/database/mocks"
	"github.com/markhc/isrv/internal/logging"
	stmocks "github.com/markhc/isrv/internal/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

func Test_Service_performCleanup_expiredFiles(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)
	expectedFiles := []string{"file1", "file2", "file3"}

	db.On("GetExpiredFiles").Return(expectedFiles, nil)
	for _, f := range expectedFiles {
		stor.On("DeleteFile", mock.Anything, f).Return(nil)
		db.On("OnFileDelete", f).Return(nil)
	}

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())
}

func Test_Service_performCleanup_noExpiredFiles(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.On("GetExpiredFiles").Return([]string{}, nil)

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())

	stor.AssertNotCalled(t, "DeleteFile")
	db.AssertNotCalled(t, "OnFileDelete")
}

func Test_Service_performCleanup_dbError(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.On("GetExpiredFiles").Return(nil, errors.New("database error"))

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())

	stor.AssertNotCalled(t, "DeleteFile")
	db.AssertNotCalled(t, "OnFileDelete")
}

func Test_Service_performCleanup_storageError(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.On("GetExpiredFiles").Return([]string{"file1"}, nil)
	stor.On("DeleteFile", mock.Anything, "file1").Return(errors.New("storage error"))
	db.On("OnFileDelete", "file1").Return(nil)

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())
}

func Test_Service_performCleanup_databaseDeleteError(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.On("GetExpiredFiles").Return([]string{"file1"}, nil)
	stor.On("DeleteFile", mock.Anything, "file1").Return(nil)
	db.On("OnFileDelete", "file1").Return(errors.New("database delete error"))

	service := NewService(db, stor, true, time.Minute)
	service.performCleanup(context.Background())
}

func Test_Service_Start_disabled(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	service := NewService(db, stor, false, time.Minute) // enabled=false

	cancel := service.Start(context.Background())
	if cancel != nil {
		cancel()
	}
	service.Join() // should not panic or block

	db.AssertNotCalled(t, "GetExpiredFiles")
}

func Test_Service_Start_enabled(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	// GetExpiredFiles may or may not be called depending on timing.
	db.On("GetExpiredFiles").Return([]string{}, nil).Maybe()

	service := NewService(db, stor, true, time.Millisecond*10)

	cancel := service.Start(context.Background())

	time.Sleep(time.Millisecond * 5)

	if cancel != nil {
		cancel()
	}
	service.Join()
}

func Test_Service_cleanupFile_success(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	stor.On("DeleteFile", mock.Anything, "test-file").Return(nil)
	db.On("OnFileDelete", "test-file").Return(nil)

	service := NewService(db, stor, true, time.Minute)

	err := service.cleanupFile(context.Background(), "test-file")

	require.NoError(t, err)
}

func Test_Service_cleanupFile_storageErrorOnly(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	stor.On("DeleteFile", mock.Anything, "test-file").Return(errors.New("storage failed"))
	db.On("OnFileDelete", "test-file").Return(nil)

	service := NewService(db, stor, true, time.Minute)

	err := service.cleanupFile(context.Background(), "test-file")

	require.Error(t, err)
}

func Test_Service_cleanupFile_databaseErrorOnly(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	stor.On("DeleteFile", mock.Anything, "test-file").Return(nil)
	db.On("OnFileDelete", "test-file").Return(errors.New("database failed"))

	service := NewService(db, stor, true, time.Minute)

	err := service.cleanupFile(context.Background(), "test-file")

	require.Error(t, err)
}

func Test_Service_cleanupFile_bothErrors(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	stor.On("DeleteFile", mock.Anything, "test-file").Return(errors.New("storage failed"))
	db.On("OnFileDelete", "test-file").Return(errors.New("database failed"))

	service := NewService(db, stor, true, time.Minute)

	err := service.cleanupFile(context.Background(), "test-file")

	// Storage error is returned as the primary error when both fail.
	assert.EqualError(t, err, "failed to delete file from storage: storage failed")
}
