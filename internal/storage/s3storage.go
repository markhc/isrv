package storage

import (
	"errors"
	"mime/multipart"
	"net/http"

	"github.com/markhc/isrv/internal/models"
)

// LocalStorage implements the Storage interface for local filesystem storage
type S3Storage struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	BasePath  string
}

func NewS3Storage(config models.StorageConfiguration) *S3Storage {
	return &S3Storage{
		Endpoint:  config.Endpoint,
		AccessKey: config.AccessKey,
		SecretKey: config.SecretKey,
		Bucket:    config.BucketName,
		Region:    config.Region,
		BasePath:  config.BasePath,
	}
}

func (s3 *S3Storage) FileExists(fileID string) (bool, error) {
	return false, errors.New("Not implemented yet")
}

func (s3 *S3Storage) SaveFile(fileID string, data []byte) (string, error) {
	return "", errors.New("Not implemented yet")
}
func (s3 *S3Storage) SaveFileUpload(fileID string, file multipart.File) (string, error) {
	return "", errors.New("Not implemented yet")
}
func (s3 *S3Storage) RetrieveFile(fileID string) ([]byte, error) {
	return nil, errors.New("Not implemented yet")
}

func (s3 *S3Storage) DeleteFile(fileID string) error {
	return errors.New("Not implemented yet")
}

func (s3 *S3Storage) ServeFile(w http.ResponseWriter, r *http.Request, fileID string, fileName string, inlineContent bool, cachingEnabled bool) {
	// Not implemented yet
	http.Error(w, "Not implemented yet", http.StatusNotImplemented)
}
