package database

import (
	"context"
	"testing"
	"time"

	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDB creates a new in-memory SQLite database for testing
func setupTestDB(t *testing.T) *SQLiteDB {
	config := models.Configuration{
		Database: models.DatabaseConfiguration{
			DSN: ":memory:",
		},
	}

	db := NewSQLiteDB(config)

	err := db.Connect()
	require.NoError(t, err, "failed to connect to test database")

	err = db.Migrate()
	require.NoError(t, err, "failed to migrate test database")

	return db
}

func Test_SQLiteDB_OnFileUpload(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tests := []struct {
		name           string
		fileID         string
		fileName       string
		fileSize       int64
		contentType    string
		expirationTime time.Time
		ipAddress      string
	}{
		{
			"basic file upload",
			"test-id-1",
			"test.txt",
			1024,
			"text/plain",
			time.Now().Add(24 * time.Hour),
			"192.168.1.1",
		},
		{
			"file without content type",
			"test-id-2",
			"unknown.bin",
			2048,
			"",
			time.Now().Add(48 * time.Hour),
			"10.0.0.1",
		},
		{
			"large file",
			"test-id-3",
			"largefile.zip",
			1024 * 1024 * 10, // 10MB
			"application/zip",
			time.Now().Add(72 * time.Hour),
			"203.0.113.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := db.OnFileUpload(context.Background(), &models.File{
				ID:          tt.fileID,
				Name:        tt.fileName,
				Size:        tt.fileSize,
				ContentType: tt.contentType,
				Expiration:  tt.expirationTime,
				IPAddress:   tt.ipAddress,
			}, tt.fileID)
			require.NoError(t, err)

			file, err := db.GetFile(context.Background(), tt.fileID)
			require.NoError(t, err)

			if tt.contentType != "" {
				assert.Equal(t, tt.contentType, file.ContentType)
			} else {
				assert.Empty(t, file.ContentType)
			}
		})
	}
}

func Test_SQLiteDB_EncryptionVersion(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	files := []models.File{
		{
			ID:                "plain-file",
			Name:              "plain.txt",
			Size:              100,
			Expiration:        time.Now().Add(24 * time.Hour),
			IPAddress:         "192.168.1.1",
			EncryptionVersion: 0,
		},
		{
			ID:                "encrypted-file",
			Name:              "secret.txt",
			Size:              200,
			Expiration:        time.Now().Add(24 * time.Hour),
			IPAddress:         "192.168.1.2",
			EncryptionVersion: 1,
		},
	}

	for i := range files {
		require.NoError(t, db.OnFileUpload(context.Background(), &files[i], files[i].ID))
	}

	t.Run("GetFile round-trips encryption version", func(t *testing.T) {
		for _, want := range files {
			got, err := db.GetFile(context.Background(), want.ID)
			require.NoError(t, err)
			assert.Equal(t, want.EncryptionVersion, got.EncryptionVersion)
		}
	})

	t.Run("ListFiles round-trips encryption version", func(t *testing.T) {
		listed, _, err := db.ListFiles(context.Background(), models.FileListFilter{})
		require.NoError(t, err)

		byID := make(map[string]int, len(listed))
		for _, f := range listed {
			byID[f.ID] = f.EncryptionVersion
		}

		assert.Equal(t, 0, byID["plain-file"])
		assert.Equal(t, 1, byID["encrypted-file"])
	})
}

func Test_SQLiteDB_OnFileDownload(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Setup: Insert a test file
	fileID := "download-test"
	err := db.OnFileUpload(context.Background(), &models.File{
		ID:         fileID,
		Name:       "test.txt",
		Size:       100,
		Expiration: time.Now().Add(24 * time.Hour),
		IPAddress:  "192.168.1.1",
	}, fileID)
	require.NoError(t, err, "setup failed")

	for i := 1; i <= 3; i++ {
		err = db.OnFileDownload(context.Background(), fileID)
		assert.NoError(t, err, "OnFileDownload() iteration %d", i)
	}

	var downloadCount int
	err = db.sqldb.Get(&downloadCount, "SELECT download_count FROM files WHERE id = ?", fileID)
	require.NoError(t, err, "failed to verify download count")
	assert.Equal(t, 3, downloadCount)

	err = db.OnFileDownload(context.Background(), "non-existing")
	assert.Error(t, err, "expected error when downloading non-existing file")
}

func Test_SQLiteDB_OnFileDelete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Setup: Insert test files
	testFiles := []string{"delete-test-1", "delete-test-2"}
	for _, fileID := range testFiles {
		err := db.OnFileUpload(context.Background(), &models.File{
			ID:         fileID,
			Name:       fileID + ".txt",
			Size:       100,
			Expiration: time.Now().Add(24 * time.Hour),
			IPAddress:  "192.168.1.1",
		}, fileID)
		require.NoError(t, err, "setup failed for %s", fileID)
	}

	err := db.OnFileDelete(context.Background(), "delete-test-1")
	require.NoError(t, err)

	_, err = db.GetFile(context.Background(), "delete-test-1")
	assert.Error(t, err, "expected error when getting deleted file")

	_, err = db.GetFile(context.Background(), "delete-test-2")
	assert.NoError(t, err, "other file should still exist")

	err = db.OnFileDelete(context.Background(), "non-existing")
	assert.Error(t, err, "expected error when deleting non-existing file")
}

func Test_SQLiteDB_GetExpiredFiles(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	baseTime := time.Now().UTC()

	testCases := []struct {
		fileID         string
		expirationTime time.Time
		shouldExpire   bool
	}{
		{"expired-unique-1", baseTime.Add(-24 * time.Hour), true},
		{"expired-unique-2", baseTime.Add(-1 * time.Hour), true},
		{"future-unique-1", baseTime.Add(24 * time.Hour), false},
		{"future-unique-2", baseTime.Add(48 * time.Hour), false},
	}

	// Setup: Insert test files with various expiration times
	for _, tc := range testCases {
		err := db.OnFileUpload(context.Background(), &models.File{
			ID:         tc.fileID,
			Name:       tc.fileID + ".txt",
			Size:       100,
			Expiration: tc.expirationTime,
			IPAddress:  "192.168.1.1",
		}, tc.fileID)
		if err != nil {
			t.Fatalf("Setup failed for %s: %v", tc.fileID, err)
		}
	}

	// Test GetExpiredFiles
	expiredFiles, err := db.GetExpiredFiles(context.Background())
	require.NoError(t, err)

	expiredSet := make(map[string]bool)
	for _, fileID := range expiredFiles {
		expiredSet[fileID] = true
	}

	assert.True(t, expiredSet["expired-unique-1"], "expired-unique-1 (24h ago) should be expired")
	assert.True(t, expiredSet["expired-unique-2"], "expired-unique-2 (1h ago) should be expired")
	assert.False(t, expiredSet["future-unique-1"], "future-unique-1 (24h in future) should not be expired")
	assert.False(t, expiredSet["future-unique-2"], "future-unique-2 (48h in future) should not be expired")
}

func Test_SQLiteDB_GetFileMetadata(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tests := []struct {
		name        string
		fileID      string
		contentType string
		expected    string
	}{
		{
			"file with content type",
			"meta-test-1",
			"application/pdf",
			"application/pdf",
		},
		{
			"file without content type",
			"meta-test-2",
			"",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup: Insert file with metadata
			err := db.OnFileUpload(context.Background(), &models.File{
				ID:          tt.fileID,
				Name:        tt.fileID + ".ext",
				Size:        100,
				ContentType: tt.contentType,
				Expiration:  time.Now().Add(24 * time.Hour),
				IPAddress:   "192.168.1.1",
			}, tt.fileID)
			require.NoError(t, err, "setup failed")

			file, err := db.GetFile(context.Background(), tt.fileID)
			require.NoError(t, err)

			assert.Equal(t, tt.expected, file.ContentType)
		})
	}
}

func Test_SQLiteDB_Connect_and_Migrate(t *testing.T) {
	// Test the DSN path (in-memory)
	config := models.Configuration{
		Database: models.DatabaseConfiguration{
			DSN: ":memory:",
		},
	}

	db := NewSQLiteDB(config)

	require.NoError(t, db.Connect())
	require.NoError(t, db.Migrate())

	assert.NoError(t, db.OnFileUpload(context.Background(), &models.File{
		ID:         "connect-test",
		Name:       "test.txt",
		Size:       100,
		Expiration: time.Now().Add(24 * time.Hour),
		IPAddress:  "192.168.1.1",
	}, ""))
	assert.NoError(t, db.Close())
}
