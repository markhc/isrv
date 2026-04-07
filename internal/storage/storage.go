package storage

//go:generate go tool mockery

import (
	"context"
	"mime/multipart"
	"net/http"
)

// Storage is the interface for file storage backends.
type Storage interface {
	// FileExists reports whether a file with the given ID exists in storage.
	FileExists(ctx context.Context, fileID string) (bool, error)
	// SaveFileUpload writes an uploaded file to storage and returns its storage path.
	SaveFileUpload(
		ctx context.Context,
		fileID string,
		file multipart.File,
		fileHeader *multipart.FileHeader) (string, error)
	// DeleteFile removes the file with the given ID from storage.
	DeleteFile(ctx context.Context, fileID string) error
	// ServeFile writes the file to the HTTP response, applying appropriate headers.
	ServeFile(
		w http.ResponseWriter,
		r *http.Request,
		fileID string,
		fileName string,
		metadata map[string]string,
		inlineContent bool,
		cachingEnabled bool)
}
