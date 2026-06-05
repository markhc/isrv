package database

//go:generate go tool mockery

import (
	"context"
	"embed"
	"errors"
	"mime/multipart"
	"time"
)

// Errors that may be returned by Database operations.
var (
	ErrFileNotFound = errors.New("file not found")
	ErrDatabase     = errors.New("database error")
	ErrConnection   = errors.New("database connection error")
)

// FileRecord represents a stored file's metadata as persisted in the
// database. The zero value is not useful; instances are produced by the
// Database implementation.
type FileRecord struct {
	ID             string    `db:"id"`
	Token          string    `db:"token"`
	FileSize       int64     `db:"file_size"`
	ExpirationTime time.Time `db:"expiration_time"`
	IPAddress      string    `db:"ip_address"`
	Metadata       map[string]string
}

// Database is the interface for all database operations used by the server.
type Database interface { //nolint:interfacebloat
	// Connect opens the database connection.
	Connect() error
	// Close releases the database connection.
	Close() error
	// Migrate applies any pending schema migrations.
	Migrate() error
	// Ping verifies the database connection is still alive. It is intended
	// for readiness probes and must be cheap and respect ctx cancellation.
	Ping(ctx context.Context) error

	// OnFileUpload records a new file upload in the database.
	OnFileUpload(
		ctx context.Context,
		fileID string,
		fileHeader *multipart.FileHeader,
		token string,
		expirationTime time.Time,
		ipAddress string) error
	// OnFileDownload increments the download counter for the given file.
	// It returns ErrFileNotFound if no record exists for the ID.
	OnFileDownload(ctx context.Context, fileID string) error
	// OnFileDelete removes the record for the given file from the database.
	// It returns ErrFileNotFound if no record exists for the ID.
	OnFileDelete(ctx context.Context, fileID string) error

	// GetFileMetadata returns the metadata map stored for the given file,
	// or ErrFileNotFound if no record exists.
	GetFileMetadata(ctx context.Context, fileID string) (map[string]string, error)
	// GetFileToken returns the token associated with the given file ID,
	// or ErrFileNotFound if no record exists.
	GetFileToken(ctx context.Context, fileID string) (string, error)
	// GetFileByToken returns the file ID associated with the given token,
	// or ErrFileNotFound if no record matches.
	GetFileByToken(ctx context.Context, token string) (string, error)
	// GetExpiredFiles returns the IDs of all files whose expiration time has passed.
	GetExpiredFiles(ctx context.Context) ([]string, error)
	// GetFileData returns the full file record for the given file ID,
	// or ErrFileNotFound if no record exists.
	GetFileData(ctx context.Context, fileID string) (*FileRecord, error)
	// SetExpiration updates the expiration time of the given file.
	// It returns ErrFileNotFound if no record exists.
	SetExpiration(ctx context.Context, fileID string, expiration time.Time) error
}

//go:embed migrations
var migrations embed.FS
