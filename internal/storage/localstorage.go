package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
)

// LocalStorage implements the Storage interface for local filesystem storage.
type LocalStorage struct {
	BasePath string
}

// NewLocalStorage creates a LocalStorage rooted at the path specified in config.
// It creates the directory if it does not already exist, and returns an error
// if the base path is inaccessible or refers to a non-directory.
func NewLocalStorage(config models.StorageConfiguration) (*LocalStorage, error) {
	switch dir, err := os.Stat(config.BasePath); {
	case os.IsNotExist(err):
		logging.LogInfo("base path does not exist, creating directory", logging.String("path", config.BasePath))

		if err := os.MkdirAll(config.BasePath, 0o755); err != nil {
			return nil, fmt.Errorf("create base directory %q: %w", config.BasePath, err)
		}
	case err != nil:
		return nil, fmt.Errorf("stat base path %q: %w", config.BasePath, err)
	case !dir.IsDir():
		return nil, fmt.Errorf("base path %q exists but is not a directory", config.BasePath)
	}

	return &LocalStorage{BasePath: config.BasePath}, nil
}

// Backend returns the backend identifier ("local").
func (ls *LocalStorage) Backend() string { return BackendLocal }

// HealthCheck verifies the configured base directory exists and is a directory.
func (ls *LocalStorage) HealthCheck(_ context.Context) error {
	info, err := os.Stat(ls.BasePath)
	if err != nil {
		return fmt.Errorf("stat base path %q: %w", ls.BasePath, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("base path %q is not a directory", ls.BasePath)
	}

	return nil
}

// FileExists reports whether a file with the given ID exists on disk.
func (ls *LocalStorage) FileExists(ctx context.Context, fileID string) (bool, error) {
	var err error
	defer recordOpDuration(ctx, BackendLocal, OperationExists, time.Now(), &err)

	filePath := filepath.Join(ls.BasePath, fileID)
	_, err = os.Stat(filePath)

	if os.IsNotExist(err) {
		err = nil

		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to check file existence: %w", err)
	}

	return true, nil
}

// Save writes r to disk under BasePath and returns the full file path.
func (ls *LocalStorage) Save(
	ctx context.Context,
	fileID string,
	r io.Reader,
	_ SaveOptions,
) (string, error) {
	var err error
	defer recordOpDuration(ctx, BackendLocal, OperationSave, time.Now(), &err)

	filePath := filepath.Join(ls.BasePath, fileID)

	dst, err := os.Create(filePath)
	if err != nil {
		logging.ErrorCtx(ctx, "failed to create file", logging.Error(err))

		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()

	_, err = io.Copy(dst, r)
	if err != nil {
		logging.ErrorCtx(ctx, "failed to copy file data", logging.Error(err))

		return "", fmt.Errorf("failed to copy file data: %w", err)
	}

	return filePath, nil
}

// DeleteFile removes the file with the given ID from disk.
func (ls *LocalStorage) DeleteFile(ctx context.Context, fileID string) error {
	var err error
	defer recordOpDuration(ctx, BackendLocal, OperationDelete, time.Now(), &err)

	filePath := filepath.Join(ls.BasePath, fileID)
	err = os.Remove(filePath)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// Open opens the stored file and returns a reader over its bytes. When brange is
// non-nil the reader is positioned to serve only the requested byte range.
func (ls *LocalStorage) Open(ctx context.Context, fileID string, brange *ByteRange) (*Object, error) {
	var err error
	defer recordOpDuration(ctx, BackendLocal, OperationServe, time.Now(), &err)

	filePath := filepath.Join(ls.BasePath, fileID)

	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			err = ErrObjectNotFound

			return nil, err
		}

		err = fmt.Errorf("failed to open file: %w", err)

		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		err = fmt.Errorf("failed to stat file: %w", err)

		return nil, err
	}

	size := info.Size()

	if brange == nil {
		return &Object{Body: file, Size: size, Length: size}, nil
	}

	start, end, err := resolveRange(brange, size)
	if err != nil {
		_ = file.Close()

		return nil, err
	}

	if _, err = file.Seek(start, io.SeekStart); err != nil {
		_ = file.Close()
		err = fmt.Errorf("failed to seek file: %w", err)

		return nil, err
	}

	length := end - start + 1

	return &Object{
		Body:         sectionReadCloser{Reader: io.LimitReader(file, length), closer: file},
		Size:         size,
		Length:       length,
		Partial:      true,
		ContentRange: fmt.Sprintf("bytes %d-%d/%d", start, end, size),
	}, nil
}

// PresignedURL reports that local storage cannot offload delivery: the caller
// must stream the bytes returned by Open.
func (ls *LocalStorage) PresignedURL(_ context.Context, _ *models.File) (string, bool, error) {
	return "", false, nil
}

// sectionReadCloser adapts a bounded Reader (an io.LimitReader over an open
// file) into an io.ReadCloser that closes the underlying file.
type sectionReadCloser struct {
	io.Reader

	closer io.Closer
}

func (s sectionReadCloser) Close() error {
	if err := s.closer.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	return nil
}

// resolveRange resolves a requested ByteRange against the actual object size,
// returning inclusive [start, end] bounds. It returns ErrInvalidRange when the
// range cannot be satisfied.
func resolveRange(brange *ByteRange, size int64) (int64, int64, error) {
	if size == 0 {
		return 0, 0, ErrInvalidRange
	}

	if brange.Suffix > 0 {
		n := min(brange.Suffix, size)

		return size - n, size - 1, nil
	}

	start := brange.Start
	if start < 0 || start >= size {
		return 0, 0, ErrInvalidRange
	}

	end := brange.End
	if end < 0 || end >= size {
		end = size - 1
	}

	if end < start {
		return 0, 0, ErrInvalidRange
	}

	return start, end, nil
}
