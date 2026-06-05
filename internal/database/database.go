package database

//go:generate go tool mockery

import (
	"context"
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
	// Ping verifies the database connection is still alive. Intended for
	// readiness probes; should be cheap and respect ctx cancellation.
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
	OnFileDownload(ctx context.Context, fileID string) error
	// OnFileDelete removes the record for the given file from the database.
	OnFileDelete(ctx context.Context, fileID string) error

	// GetFileMetadata returns the metadata map stored for the given file.
	GetFileMetadata(ctx context.Context, fileID string) (map[string]string, error)
	// GetFileToken returns the token associated with the given file ID.
	GetFileToken(ctx context.Context, fileID string) (string, error)
	// GetFileByToken returns the file ID associated with the given token.
	GetFileByToken(ctx context.Context, token string) (string, error)
	// GetExpiredFiles returns the IDs of all files whose expiration time has passed.
	GetExpiredFiles(ctx context.Context) ([]string, error)
	// GetFileData returns the file record for the given file ID.
	GetFileData(ctx context.Context, fileID string) (*FileRecord, error)
	// SetExpiration updates the expiration time of the given file.
	SetExpiration(ctx context.Context, fileID string, expiration time.Time) error
}

//go:embed migrations
var migrations embed.FS
