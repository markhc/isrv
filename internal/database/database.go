package database

import (
	"embed"
	"time"
)

type Database interface {
	Connect() error
	Close() error
	Migrate() error

	OnFileUpload(fileID string, fileName string, fileSize int64, expirationTime time.Time, ipAddress string) error
	OnFileDownload(fileID string) error
	OnFileDelete(fileID string) error
}

//go:embed migrations
var migrations embed.FS
