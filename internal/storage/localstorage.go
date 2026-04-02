package storage

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"

	"github.com/markhc/isrv/internal/headers"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
)

// LocalStorage implements the Storage interface for local filesystem storage.
type LocalStorage struct {
	BasePath string
}

// NewLocalStorage creates a LocalStorage rooted at the path specified in config.
// It creates the directory if it does not already exist and panics on any
// unrecoverable error.
func NewLocalStorage(config models.StorageConfiguration) *LocalStorage {
	if dir, err := os.Stat(config.BasePath); os.IsNotExist(err) {
		logging.LogInfo("base path does not exist, creating directory", logging.String("path", config.BasePath))

		err := os.MkdirAll(config.BasePath, 0o755)
		if err != nil {
			logging.LogFatal("failed to create base directory", logging.Error(err))
		}
	} else if err != nil {
		logging.LogFatal("failed to access base path", logging.Error(err))
	} else if !dir.IsDir() {
		logging.LogFatal("base path exists but is not a directory", logging.String("path", config.BasePath))
	}

	return &LocalStorage{BasePath: config.BasePath}
}

// FileExists reports whether a file with the given ID exists on disk.
func (ls *LocalStorage) FileExists(ctx context.Context, fileID string) (bool, error) {
	filePath := path.Join(ls.BasePath, fileID)
	_, err := os.Stat(filePath)

	if os.IsNotExist(err) {
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
	filePath := path.Join(ls.BasePath, fileID)

	dst, err := os.Create(filePath)
	if err != nil {
		logging.LogError("failed to create file", logging.Error(err))

		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()

	_, err = io.Copy(dst, file)
	if err != nil {
		logging.LogError("failed to copy file data", logging.Error(err))

		return "", fmt.Errorf("failed to copy file data: %w", err)
	}

	return filePath, nil
}

// DeleteFile removes the file with the given ID from disk.
func (ls *LocalStorage) DeleteFile(ctx context.Context, fileID string) error {
	filePath := path.Join(ls.BasePath, fileID)
	err := os.Remove(filePath)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// ServeFile sets response headers and serves the file directly from disk.
func (ls *LocalStorage) ServeFile(
	w http.ResponseWriter,
	r *http.Request,
	fileID string,
	fileName string,
	metadata map[string]string,
	inlineContent bool,
	cachingEnabled bool,
) {
	headers.SetHeaders(w, fileName, metadata, inlineContent, cachingEnabled)
	http.ServeFile(w, r, path.Join(ls.BasePath, fileID))
}
