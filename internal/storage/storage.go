package storage

import (
	"mime/multipart"
	"net/http"
)

type Storage interface {
	FileExists(fileID string) (bool, error)
	SaveFileUpload(fileID string, file multipart.File, fileHeader *multipart.FileHeader) (string, error)
	RetrieveFile(fileID string) ([]byte, error)
	DeleteFile(fileID string) error
	ServeFile(w http.ResponseWriter, r *http.Request, fileID string, fileName string, metadata map[string]string, inlineContent bool, cachingEnabled bool)
}
