package storage

import (
	"context"
	"mime/multipart"
	"net/http"
)

type Storage interface {
	FileExists(ctx context.Context, fileID string) (bool, error)
	SaveFileUpload(ctx context.Context, fileID string, file multipart.File, fileHeader *multipart.FileHeader) (string, error)
	RetrieveFile(ctx context.Context, fileID string) ([]byte, error)
	DeleteFile(ctx context.Context, fileID string) error
	ServeFile(w http.ResponseWriter, r *http.Request, fileID string, fileName string, metadata map[string]string, inlineContent bool, cachingEnabled bool)
}
