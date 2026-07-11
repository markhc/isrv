package cleanup

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/markhc/isrv/internal/database"
	dbmocks "github.com/markhc/isrv/internal/database/mocks"
	"github.com/markhc/isrv/internal/logging"
	stmocks "github.com/markhc/isrv/internal/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// PostgresDB must provide the optional cycle-locking capability discovered by
// type assertion when wiring the cleanup service.
var _ CycleLocker = (*database.PostgresDB)(nil)

// fakeCycleLocker is a trivial hand-written CycleLocker double (a mockery
// mock would force exporting runCleanupCycle to an external test package).
type fakeCycleLocker struct {
	acquired bool
	err      error
	released bool
}

func (f *fakeCycleLocker) TryLock(context.Context) (func(), bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}

	if !f.acquired {
		return nil, false, nil
	}

	return func() { f.released = true }, true, nil
}

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

func Test_Service_performCleanup_expiredFiles(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)
	expectedFiles := []string{"file1", "file2", "file3"}

	db.EXPECT().GetExpiredFiles(mock.Anything).Return(expectedFiles, nil)
	for _, f := range expectedFiles {
		stor.EXPECT().DeleteFile(mock.Anything, f).Return(nil)
		db.EXPECT().OnFileDelete(mock.Anything, f).Return(nil)
	}

	service := NewService(db, stor, true, time.Minute, nil)
	service.performCleanup(context.Background())
}

func Test_Service_performCleanup_noExpiredFiles(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.EXPECT().GetExpiredFiles(mock.Anything).Return([]string{}, nil)

	service := NewService(db, stor, true, time.Minute, nil)
	service.performCleanup(context.Background())

	stor.AssertNotCalled(t, "DeleteFile")
	db.AssertNotCalled(t, "OnFileDelete")
}

func Test_Service_performCleanup_dbError(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.EXPECT().GetExpiredFiles(mock.Anything).Return(nil, errors.New("database error"))

	service := NewService(db, stor, true, time.Minute, nil)
	service.performCleanup(context.Background())

	stor.AssertNotCalled(t, "DeleteFile")
	db.AssertNotCalled(t, "OnFileDelete")
}

func Test_Service_performCleanup_storageError(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.EXPECT().GetExpiredFiles(mock.Anything).Return([]string{"file1"}, nil)
	stor.EXPECT().DeleteFile(mock.Anything, "file1").Return(errors.New("storage error"))
	db.EXPECT().OnFileDelete(mock.Anything, "file1").Return(nil)

	service := NewService(db, stor, true, time.Minute, nil)
	service.performCleanup(context.Background())
}

func Test_Service_performCleanup_databaseDeleteError(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.EXPECT().GetExpiredFiles(mock.Anything).Return([]string{"file1"}, nil)
	stor.EXPECT().DeleteFile(mock.Anything, "file1").Return(nil)
	db.EXPECT().OnFileDelete(mock.Anything, "file1").Return(errors.New("database delete error"))

	service := NewService(db, stor, true, time.Minute, nil)
	service.performCleanup(context.Background())
}

func Test_Service_Start_disabled(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	service := NewService(db, stor, false, time.Minute, nil) // enabled=false

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
	db.EXPECT().GetExpiredFiles(mock.Anything).Return([]string{}, nil).Maybe()

	service := NewService(db, stor, true, time.Millisecond*10, nil)

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

	stor.EXPECT().DeleteFile(mock.Anything, "test-file").Return(nil)
	db.EXPECT().OnFileDelete(mock.Anything, "test-file").Return(nil)

	service := NewService(db, stor, true, time.Minute, nil)

	err := service.cleanupFile(context.Background(), "test-file")

	require.NoError(t, err)
}

func Test_Service_cleanupFile_storageErrorOnly(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	stor.EXPECT().DeleteFile(mock.Anything, "test-file").Return(errors.New("storage failed"))
	db.EXPECT().OnFileDelete(mock.Anything, "test-file").Return(nil)

	service := NewService(db, stor, true, time.Minute, nil)

	err := service.cleanupFile(context.Background(), "test-file")

	require.Error(t, err)
}

func Test_Service_cleanupFile_databaseErrorOnly(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	stor.EXPECT().DeleteFile(mock.Anything, "test-file").Return(nil)
	db.EXPECT().OnFileDelete(mock.Anything, "test-file").Return(errors.New("database failed"))

	service := NewService(db, stor, true, time.Minute, nil)

	err := service.cleanupFile(context.Background(), "test-file")

	require.Error(t, err)
}

func Test_Service_cleanupFile_bothErrors(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	stor.EXPECT().DeleteFile(mock.Anything, "test-file").Return(errors.New("storage failed"))
	db.EXPECT().OnFileDelete(mock.Anything, "test-file").Return(errors.New("database failed"))

	service := NewService(db, stor, true, time.Minute, nil)

	err := service.cleanupFile(context.Background(), "test-file")

	// Storage error is returned as the primary error when both fail.
	assert.EqualError(t, err, "failed to delete file from storage: storage failed")
}

func Test_Service_runCleanupCycle_nilLockerDefaultsToNop(t *testing.T) {
	db := dbmocks.NewMockDatabase(t)
	stor := stmocks.NewMockStorage(t)

	db.EXPECT().GetExpiredFiles(mock.Anything).Return([]string{}, nil)

	service := NewService(db, stor, true, time.Minute, nil)
	service.runCleanupCycle(context.Background())
}

func Test_Service_runCleanupCycle_locking(t *testing.T) {
	tests := []struct {
		name           string
		locker         *fakeCycleLocker
		expectCycleRun bool
		expectReleased bool
	}{
		{
			name:           "acquired lock runs the cycle and releases",
			locker:         &fakeCycleLocker{acquired: true},
			expectCycleRun: true,
			expectReleased: true,
		},
		{
			name:           "lock held elsewhere skips the cycle",
			locker:         &fakeCycleLocker{acquired: false},
			expectCycleRun: false,
			expectReleased: false,
		},
		{
			name:           "lock error skips the cycle",
			locker:         &fakeCycleLocker{err: errors.New("lock error")},
			expectCycleRun: false,
			expectReleased: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := dbmocks.NewMockDatabase(t)
			stor := stmocks.NewMockStorage(t)

			if tt.expectCycleRun {
				db.EXPECT().GetExpiredFiles(mock.Anything).Return([]string{}, nil)
			}

			service := NewService(db, stor, true, time.Minute, tt.locker)
			service.runCleanupCycle(context.Background())

			if !tt.expectCycleRun {
				db.AssertNotCalled(t, "GetExpiredFiles")
			}

			assert.Equal(t, tt.expectReleased, tt.locker.released)
		})
	}
}
