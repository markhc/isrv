package storage

import (
	"bytes"
	"context"
	"io"
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
		ls, err := NewLocalStorage(cfg)
		require.NoError(t, err)
		assert.Equal(t, dir, ls.BasePath)
		_, err = os.Stat(dir)
		assert.False(t, os.IsNotExist(err), "NewLocalStorage() did not create the base directory")
	})

	t.Run("accepts existing directory", func(t *testing.T) {
		dir := t.TempDir()
		cfg := models.StorageConfiguration{BasePath: dir}
		ls, err := NewLocalStorage(cfg)
		require.NoError(t, err)
		assert.Equal(t, dir, ls.BasePath)
	})

	t.Run("returns error when base path is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "notadir")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
		cfg := models.StorageConfiguration{BasePath: file}
		ls, err := NewLocalStorage(cfg)
		require.Error(t, err)
		assert.Nil(t, ls)
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

// mockMultipartFile implements multipart.File interface for testing
type mockMultipartFile struct {
	*bytes.Reader
}

func (m *mockMultipartFile) Close() error {
	return nil
}

func Test_LocalStorage_Open(t *testing.T) {
	tempDir := t.TempDir()
	ls := &LocalStorage{BasePath: tempDir}
	ctx := context.Background()

	content := []byte("hello local world") // 17 bytes
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "file.txt"), content, 0o644))

	t.Run("full read", func(t *testing.T) {
		obj, err := ls.Open(ctx, "file.txt", nil)
		require.NoError(t, err)
		defer obj.Body.Close()

		got, err := io.ReadAll(obj.Body)
		require.NoError(t, err)
		assert.Equal(t, content, got)
		assert.Equal(t, int64(len(content)), obj.Size)
		assert.Equal(t, int64(len(content)), obj.Length)
		assert.False(t, obj.Partial)
	})

	t.Run("bounded range", func(t *testing.T) {
		obj, err := ls.Open(ctx, "file.txt", &ByteRange{Start: 0, End: 4})
		require.NoError(t, err)
		defer obj.Body.Close()

		got, err := io.ReadAll(obj.Body)
		require.NoError(t, err)
		assert.Equal(t, content[:5], got)
		assert.True(t, obj.Partial)
		assert.Equal(t, int64(5), obj.Length)
		assert.Equal(t, "bytes 0-4/17", obj.ContentRange)
	})

	t.Run("open-ended range", func(t *testing.T) {
		obj, err := ls.Open(ctx, "file.txt", &ByteRange{Start: 6, End: -1})
		require.NoError(t, err)
		defer obj.Body.Close()

		got, err := io.ReadAll(obj.Body)
		require.NoError(t, err)
		assert.Equal(t, content[6:], got)
		assert.Equal(t, "bytes 6-16/17", obj.ContentRange)
	})

	t.Run("suffix range", func(t *testing.T) {
		obj, err := ls.Open(ctx, "file.txt", &ByteRange{Suffix: 5})
		require.NoError(t, err)
		defer obj.Body.Close()

		got, err := io.ReadAll(obj.Body)
		require.NoError(t, err)
		assert.Equal(t, content[len(content)-5:], got)
		assert.Equal(t, "bytes 12-16/17", obj.ContentRange)
	})

	t.Run("missing file maps to ErrObjectNotFound", func(t *testing.T) {
		_, err := ls.Open(ctx, "nope.txt", nil)
		assert.ErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("range beyond eof maps to ErrInvalidRange", func(t *testing.T) {
		_, err := ls.Open(ctx, "file.txt", &ByteRange{Start: 100, End: -1})
		assert.ErrorIs(t, err, ErrInvalidRange)
	})
}

func Test_LocalStorage_PresignedURL(t *testing.T) {
	ls := &LocalStorage{BasePath: t.TempDir()}
	url, ok, err := ls.PresignedURL(context.Background(), &models.File{ID: "abc"})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, url)
}
