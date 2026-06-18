package storage

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/headers"
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

	filePath := path.Join(ls.BasePath, fileID)
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

// SaveFileUpload writes the uploaded file to disk under BasePath and returns the full file path.
func (ls *LocalStorage) SaveFileUpload(
	ctx context.Context,
	fileID string,
	file multipart.File,
	_ *multipart.FileHeader,
) (string, error) {
	var err error
	defer recordOpDuration(ctx, BackendLocal, OperationSave, time.Now(), &err)

	filePath := path.Join(ls.BasePath, fileID)

	dst, err := os.Create(filePath)
	if err != nil {
		logging.ErrorCtx(ctx, "failed to create file", logging.Error(err))

		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()

	_, err = io.Copy(dst, file)
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

	filePath := path.Join(ls.BasePath, fileID)
	err = os.Remove(filePath)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// ServeFile sets response headers and serves the file directly from disk.
func (ls *LocalStorage) ServeFile(
	c fiber.Ctx,
	fileID string,
	fileName string,
	metadata map[string]string,
	inlineContent bool,
	cachingEnabled bool,
) error {
	headers.SetHeaders(c, fileName, metadata, inlineContent, cachingEnabled)
	return c.SendFile(path.Join(ls.BasePath, fileID), fiber.SendFile{ByteRange: true})
}
