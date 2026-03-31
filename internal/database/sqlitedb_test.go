package database

import (
	"mime/multipart"
	"net/textproto"
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
			// Create mock multipart.FileHeader
			header := &multipart.FileHeader{
				Filename: tt.fileName,
				Size:     tt.fileSize,
				Header:   make(textproto.MIMEHeader),
			}
			if tt.contentType != "" {
				header.Header.Set("Content-Type", tt.contentType)
			}

			err := db.OnFileUpload(tt.fileID, header, tt.expirationTime, tt.ipAddress)
			require.NoError(t, err)

			metadata, err := db.GetFileMetadata(tt.fileID)
			require.NoError(t, err)

			if tt.contentType != "" {
				assert.Equal(t, tt.contentType, metadata["Content-Type"])
			} else {
				assert.NotContains(t, metadata, "Content-Type")
			}
		})
	}
}

func Test_SQLiteDB_OnFileDownload(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Setup: Insert a test file
	fileID := "download-test"
	header := &multipart.FileHeader{
		Filename: "test.txt",
		Size:     100,
		Header:   make(textproto.MIMEHeader),
	}
	err := db.OnFileUpload(fileID, header, time.Now().Add(24*time.Hour), "192.168.1.1")
	require.NoError(t, err, "setup failed")

	for i := 1; i <= 3; i++ {
		err = db.OnFileDownload(fileID)
		assert.NoError(t, err, "OnFileDownload() iteration %d", i)
	}

	var downloadCount int
	err = db.sqldb.Get(&downloadCount, "SELECT download_count FROM files WHERE id = ?", fileID)
	require.NoError(t, err, "failed to verify download count")
	assert.Equal(t, 3, downloadCount)

	err = db.OnFileDownload("non-existing")
	assert.NoError(t, err, "OnFileDownload() for non-existing file should not error")
}

func Test_SQLiteDB_OnFileDelete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Setup: Insert test files
	testFiles := []string{"delete-test-1", "delete-test-2"}
	for _, fileID := range testFiles {
		header := &multipart.FileHeader{
			Filename: fileID + ".txt",
			Size:     100,
			Header:   make(textproto.MIMEHeader),
		}
		err := db.OnFileUpload(fileID, header, time.Now().Add(24*time.Hour), "192.168.1.1")
		require.NoError(t, err, "setup failed for %s", fileID)
	}

	err := db.OnFileDelete("delete-test-1")
	require.NoError(t, err)

	_, err = db.GetFileMetadata("delete-test-1")
	assert.Error(t, err, "expected error when getting metadata for deleted file")

	_, err = db.GetFileMetadata("delete-test-2")
	assert.NoError(t, err, "other file should still exist")

	err = db.OnFileDelete("non-existing")
	assert.NoError(t, err, "OnFileDelete() for non-existing file should not error")
}

func Test_SQLiteDB_GetExpiredFiles(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	baseTime := time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)

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
		header := &multipart.FileHeader{
			Filename: tc.fileID + ".txt",
			Size:     100,
			Header:   make(textproto.MIMEHeader),
		}
		err := db.OnFileUpload(tc.fileID, header, tc.expirationTime, "192.168.1.1")
		if err != nil {
			t.Fatalf("Setup failed for %s: %v", tc.fileID, err)
		}
	}

	// Test GetExpiredFiles
	expiredFiles, err := db.GetExpiredFiles()
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
		expected    map[string]string
	}{
		{
			"file with content type",
			"meta-test-1",
			"application/pdf",
			map[string]string{"Content-Type": "application/pdf"},
		},
		{
			"file without content type",
			"meta-test-2",
			"",
			map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup: Insert file with metadata
			header := &multipart.FileHeader{
				Filename: tt.fileID + ".ext",
				Size:     100,
				Header:   make(textproto.MIMEHeader),
			}
			if tt.contentType != "" {
				header.Header.Set("Content-Type", tt.contentType)
			}

			err := db.OnFileUpload(tt.fileID, header, time.Now().Add(24*time.Hour), "192.168.1.1")
			require.NoError(t, err, "setup failed")

			metadata, err := db.GetFileMetadata(tt.fileID)
			require.NoError(t, err)

			assert.Equal(t, tt.expected, metadata)
		})
	}
}

func Test_SQLiteDB_GetFileMetadata_nonExist(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, err := db.GetFileMetadata("non-existing-file")
	assert.Error(t, err)
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

	header := &multipart.FileHeader{
		Filename: "test.txt",
		Size:     100,
		Header:   make(textproto.MIMEHeader),
	}
	assert.NoError(t, db.OnFileUpload("connect-test", header, time.Now().Add(24*time.Hour), "192.168.1.1"))
	assert.NoError(t, db.Close())
}
