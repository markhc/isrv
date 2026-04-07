package database

//go:generate go tool mockery

import (
	"embed"
	"mime/multipart"
	"time"
)

// Database is the interface for all database operations used by the server.
type Database interface {
	// Connect opens the database connection.
	Connect() error
	// Close releases the database connection.
	Close() error
	// Migrate applies any pending schema migrations.
	Migrate() error

	// OnFileUpload records a new file upload in the database.
	OnFileUpload(fileID string, fileHeader *multipart.FileHeader, expirationTime time.Time, ipAddress string) error
	// OnFileDownload increments the download counter for the given file.
	OnFileDownload(fileID string) error
	// OnFileDelete removes the record for the given file from the database.
	OnFileDelete(fileID string) error

	// GetFileMetadata returns the metadata map stored for the given file.
	GetFileMetadata(fileID string) (map[string]string, error)
	// GetExpiredFiles returns the IDs of all files whose expiration time has passed.
	GetExpiredFiles() ([]string, error)
}

//go:embed migrations
var migrations embed.FS
