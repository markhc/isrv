package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

func Test_NewLocalStorage(t *testing.T) {
	t.Run("creates directory if not exists", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "newdir")
		cfg := models.StorageConfiguration{BasePath: dir}
		ls := NewLocalStorage(cfg)
		assert.Equal(t, dir, ls.BasePath)
		_, err := os.Stat(dir)
		assert.False(t, os.IsNotExist(err), "NewLocalStorage() did not create the base directory")
	})

	t.Run("accepts existing directory", func(t *testing.T) {
		dir := t.TempDir()
		cfg := models.StorageConfiguration{BasePath: dir}
		ls := NewLocalStorage(cfg)
		assert.Equal(t, dir, ls.BasePath)
	})
}

func Test_LocalStorage_FileExists(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	ls := &LocalStorage{BasePath: tempDir}
	ctx := context.Background()

	// Create a test file
	testFileID := "test-file.txt"
	testFilePath := filepath.Join(tempDir, testFileID)
	err := os.WriteFile(testFilePath, []byte("test content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tests := []struct {
		name     string
		fileID   string
		expected bool
		wantErr  bool
	}{
		{"existing file", testFileID, true, false},
		{"non-existing file", "non-existing.txt", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := ls.FileExists(ctx, tt.fileID)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, exists)
		})
	}
}

func Test_LocalStorage_SaveFileUpload_and_RetrieveFile(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	ls := &LocalStorage{BasePath: tempDir}
	ctx := context.Background()

	tests := []struct {
		name    string
		fileID  string
		content string
	}{
		{"simple file", "simple.txt", "Hello, World!"},
		{"empty file", "empty.txt", ""},
		{"binary content", "binary.bin", "\x00\x01\x02\x03\xFF\xFE\xFD"},
		{"large content", "large.txt", string(bytes.Repeat([]byte("A"), 1024))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create multipart.File from test content
			content := []byte(tt.content)

			// Create a mock multipart.File
			mockFile := &mockMultipartFile{Reader: bytes.NewReader(content)}

			// Test SaveFileUpload
			storedPath, err := ls.SaveFileUpload(ctx, tt.fileID, mockFile, nil)
			require.NoError(t, err)

			expectedPath := filepath.Join(tempDir, tt.fileID)
			assert.Equal(t, expectedPath, storedPath)

			exists, err := ls.FileExists(ctx, tt.fileID)
			require.NoError(t, err)
			assert.True(t, exists, "file should exist after SaveFileUpload()")

			retrievedContent, err := ls.RetrieveFile(ctx, tt.fileID)
			require.NoError(t, err)
			assert.Equal(t, content, retrievedContent)
		})
	}
}

func Test_LocalStorage_DeleteFile(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	ls := &LocalStorage{BasePath: tempDir}
	ctx := context.Background()

	// Create test files
	testFiles := []string{"file1.txt", "file2.txt", "subdir/file3.txt"}
	for _, fileID := range testFiles {
		filePath := filepath.Join(tempDir, fileID)

		// Create directory if needed
		dir := filepath.Dir(filePath)
		if dir != tempDir {
			err := os.MkdirAll(dir, 0755)
			if err != nil {
				t.Fatalf("Failed to create directory %s: %v", dir, err)
			}
		}

		err := os.WriteFile(filePath, []byte("test content"), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", fileID, err)
		}
	}

	tests := []struct {
		name    string
		fileID  string
		wantErr bool
	}{
		{"delete existing file", "file1.txt", false},
		{"delete file in subdirectory", "subdir/file3.txt", false},
		{"delete non-existing file", "non-existing.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ls.DeleteFile(ctx, tt.fileID)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			exists, err := ls.FileExists(ctx, tt.fileID)
			require.NoError(t, err)
			assert.False(t, exists, "file %s should not exist after DeleteFile()", tt.fileID)
		})
	}
}

func Test_LocalStorage_RetrieveFile_nonExist(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	ls := &LocalStorage{BasePath: tempDir}
	ctx := context.Background()

	_, err := ls.RetrieveFile(ctx, "non-existing.txt")
	require.Error(t, err)
	assert.True(t, os.IsNotExist(err), "expected os.IsNotExist error, got %T: %v", err, err)
}

// mockMultipartFile implements multipart.File interface for testing
type mockMultipartFile struct {
	*bytes.Reader
}

func (m *mockMultipartFile) Close() error {
	return nil
}
