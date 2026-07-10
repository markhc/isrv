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

func Test_LocalStorage_HealthCheck(t *testing.T) {
	t.Run("healthy when base path is a directory", func(t *testing.T) {
		ls := &LocalStorage{BasePath: t.TempDir()}
		assert.NoError(t, ls.HealthCheck(context.Background()))
	})

	t.Run("unhealthy when base path does not exist", func(t *testing.T) {
		ls := &LocalStorage{BasePath: filepath.Join(t.TempDir(), "missing")}
		err := ls.HealthCheck(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stat base path")
	})

	t.Run("unhealthy when base path is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
		ls := &LocalStorage{BasePath: file}
		err := ls.HealthCheck(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not a directory")
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
	err := os.WriteFile(testFilePath, []byte("test content"), 0o644)
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

func Test_LocalStorage_Save(t *testing.T) {
	tests := []struct {
		name    string
		content string
		opts    SaveOptions
		wantErr bool
	}{
		{name: "known size matches", content: "hello", opts: SaveOptions{Size: 5}},
		{name: "unknown size", content: "hello", opts: SaveOptions{Size: -1}},
		{name: "size mismatch", content: "hello", opts: SaveOptions{Size: 10}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &LocalStorage{BasePath: t.TempDir()}

			path, err := ls.Save(context.Background(), "test-id", bytes.NewReader([]byte(tt.content)), tt.opts)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tt.content, string(data))
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
			err := os.MkdirAll(dir, 0o755)
			if err != nil {
				t.Fatalf("Failed to create directory %s: %v", dir, err)
			}
		}

		err := os.WriteFile(filePath, []byte("test content"), 0o644)
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

	tests := []struct {
		name         string
		fileID       string
		brange       *ByteRange
		wantBody     []byte
		wantSize     int64
		wantLength   int64
		wantPartial  bool
		wantCRange   string
		wantErr      bool
		wantSentinel error
	}{
		{
			name:       "full read",
			fileID:     "file.txt",
			wantBody:   content,
			wantSize:   int64(len(content)),
			wantLength: int64(len(content)),
		},
		{
			name:        "bounded range",
			fileID:      "file.txt",
			brange:      &ByteRange{Start: 0, End: 4},
			wantBody:    content[:5],
			wantLength:  5,
			wantPartial: true,
			wantCRange:  "bytes 0-4/17",
		},
		{
			name:        "open-ended range",
			fileID:      "file.txt",
			brange:      &ByteRange{Start: 6, End: -1},
			wantBody:    content[6:],
			wantLength:  int64(len(content) - 6),
			wantPartial: true,
			wantCRange:  "bytes 6-16/17",
		},
		{
			name:        "suffix range",
			fileID:      "file.txt",
			brange:      &ByteRange{Suffix: 5},
			wantBody:    content[len(content)-5:],
			wantLength:  5,
			wantPartial: true,
			wantCRange:  "bytes 12-16/17",
		},
		{
			name:         "missing file maps to ErrObjectNotFound",
			fileID:       "nope.txt",
			wantErr:      true,
			wantSentinel: ErrObjectNotFound,
		},
		{
			name:         "range beyond eof maps to ErrInvalidRange",
			fileID:       "file.txt",
			brange:       &ByteRange{Start: 100, End: -1},
			wantErr:      true,
			wantSentinel: ErrInvalidRange,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := ls.Open(ctx, tt.fileID, tt.brange)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantSentinel != nil {
					assert.ErrorIs(t, err, tt.wantSentinel)
				}
				return
			}

			require.NoError(t, err)
			defer obj.Body.Close()

			got, readErr := io.ReadAll(obj.Body)
			require.NoError(t, readErr)
			assert.Equal(t, tt.wantBody, got)
			assert.Equal(t, tt.wantLength, obj.Length)
			assert.Equal(t, tt.wantPartial, obj.Partial)
			if tt.wantSize != 0 {
				assert.Equal(t, tt.wantSize, obj.Size)
			}
			if tt.wantCRange != "" {
				assert.Equal(t, tt.wantCRange, obj.ContentRange)
			}
		})
	}
}

func Test_LocalStorage_PresignedURL(t *testing.T) {
	ls := &LocalStorage{BasePath: t.TempDir()}
	url, ok, err := ls.PresignedURL(context.Background(), &models.File{ID: "abc"})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, url)
}

func Test_resolveRange(t *testing.T) {
	tests := []struct {
		name      string
		brange    ByteRange
		size      int64
		wantStart int64
		wantEnd   int64
		wantErr   bool
	}{
		{name: "zero-size object is unsatisfiable", brange: ByteRange{Start: 0, End: 0}, size: 0, wantErr: true},
		{name: "suffix within size", brange: ByteRange{Suffix: 5}, size: 17, wantStart: 12, wantEnd: 16},
		{name: "suffix larger than size clamps to whole object", brange: ByteRange{Suffix: 100}, size: 17, wantStart: 0, wantEnd: 16},
		{name: "bounded range", brange: ByteRange{Start: 0, End: 4}, size: 17, wantStart: 0, wantEnd: 4},
		{name: "open-ended range clamps to eof", brange: ByteRange{Start: 6, End: -1}, size: 17, wantStart: 6, wantEnd: 16},
		{name: "end beyond eof clamps to eof", brange: ByteRange{Start: 2, End: 999}, size: 17, wantStart: 2, wantEnd: 16},
		{name: "start beyond eof is invalid", brange: ByteRange{Start: 100, End: -1}, size: 17, wantErr: true},
		{name: "negative start is invalid", brange: ByteRange{Start: -1, End: 4}, size: 17, wantErr: true},
		{name: "end before start is invalid", brange: ByteRange{Start: 5, End: 3}, size: 17, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := resolveRange(&tt.brange, tt.size)
			if tt.wantErr {
				assert.ErrorIs(t, err, ErrInvalidRange)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantStart, start)
			assert.Equal(t, tt.wantEnd, end)
		})
	}
}
