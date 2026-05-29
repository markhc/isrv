package database

//go:generate go tool mockery

import (
	"embed"
	"errors"
	"mime/multipart"
	"time"
)

// Define common errors that might be returned by database operations.
var (
	ErrFileNotFound = errors.New("file not found")
	ErrDatabase     = errors.New("database error")
	ErrConnection   = errors.New("database connection error")
)

type FileRecord struct {
	ID             string            `db:"id"`
	Token          string            `db:"token"`
	FileSize       int64             `db:"file_size"`
	Metadata       map[string]string `db:"metadata"`
	ExpirationTime time.Time         `db:"expiration_time"`
	IPAddress      string            `db:"ip_address"`
}

// Database is the interface for all database operations used by the server.
type Database interface { //nolint:interfacebloat
	// Connect opens the database connection.
	Connect() error
	// Close releases the database connection.
	Close() error
	// Migrate applies any pending schema migrations.
	Migrate() error

	// OnFileUpload records a new file upload in the database.
	OnFileUpload(
		fileID string,
		fileHeader *multipart.FileHeader,
		token string,
		expirationTime time.Time,
		ipAddress string) error
	// OnFileDownload increments the download counter for the given file.
	OnFileDownload(fileID string) error
	// OnFileDelete removes the record for the given file from the database.
	OnFileDelete(fileID string) error

	// GetFileMetadata returns the metadata map stored for the given file.
	GetFileMetadata(fileID string) (map[string]string, error)
	// GetFileToken returns the token associated with the given file ID.
	GetFileToken(fileID string) (string, error)
	// GetFileByToken returns the file ID associated with the given token.
	GetFileByToken(token string) (string, error)
	// GetExpiredFiles returns the IDs of all files whose expiration time has passed.
	GetExpiredFiles() ([]string, error)
	// GetFileData returns the file record for the given file ID.
	GetFileData(fileID string) (*FileRecord, error)
	// SetExpiration updates the expiration time of the given file.
	SetExpiration(fileID string, expiration time.Time) error
}

//go:embed migrations
var migrations embed.FS
