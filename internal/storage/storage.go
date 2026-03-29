package storage

import (
	"mime/multipart"
	"net/http"
)

type Storage interface {
	FileExists(fileID string) (bool, error)
	SaveFile(fileID string, data []byte) (string, error)
	SaveFileUpload(fileID string, file multipart.File) (string, error)
	RetrieveFile(fileID string) ([]byte, error)
	DeleteFile(fileID string) error
	ServeFile(w http.ResponseWriter, r *http.Request, fileID string, fileName string, inlineContent bool, cachingEnabled bool)
}
