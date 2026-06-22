package database

//go:generate go tool mockery

import (
	"context"
	"embed"
	"errors"
	"mime/multipart"
	"time"

	"github.com/markhc/isrv/internal/models"
)

// Errors that may be returned by Database operations.
var (
	ErrFileNotFound = errors.New("file not found")
	ErrDatabase     = errors.New("database error")
	ErrConnection   = errors.New("database connection error")
)

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
	// GetFileToken returns the token associated with the given file ID,
	// or ErrFileNotFound if no record exists.
	GetFileToken(ctx context.Context, fileID string) (string, error)
	// GetFileByToken returns the file ID associated with the given token,
	// or ErrFileNotFound if no record matches.
	GetFileByToken(ctx context.Context, token string) (string, error)
	// GetExpiredFiles returns the IDs of all files whose expiration time has passed.
	GetExpiredFiles(ctx context.Context) ([]string, error)
	// GetFile returns the full file record for the given file ID,
	// or ErrFileNotFound if no record exists.
	GetFile(ctx context.Context, fileID string) (*models.File, error)
	// ListFiles returns a page of file records matching filter, ordered as
	// requested, together with the total count of matching records (ignoring
	// pagination). It is used by the admin panel.
	ListFiles(ctx context.Context, filter models.FileListFilter) ([]models.File, int, error)
	// SetExpiration updates the expiration time of the given file.
	// It returns ErrFileNotFound if no record exists.
	SetExpiration(ctx context.Context, fileID string, expiration time.Time) error
}

//go:embed migrations
var migrations embed.FS
