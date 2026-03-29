package database

import (
	"embed"
	"mime/multipart"
	"time"
)

type Database interface {
	Connect() error
	Close() error
	Migrate() error

	OnFileUpload(fileID string, fileHeader *multipart.FileHeader, expirationTime time.Time, ipAddress string) error
	OnFileDownload(fileID string) error
	OnFileDelete(fileID string) error

	GetFileMetadata(fileID string) (map[string]string, error)
	GetExpiredFiles() ([]string, error)
}

//go:embed migrations
var migrations embed.FS
