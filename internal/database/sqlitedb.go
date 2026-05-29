package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	"github.com/markhc/isrv/internal/models"
	_ "modernc.org/sqlite"
)

// SQLiteDB implements Database using a SQLite backend.
type SQLiteDB struct {
	filePath  string
	pathIsDSN bool

	sqldb *sqlx.DB
}

const (
	QUERY_INSERT_FILE = `
		INSERT INTO files (id, file_name, file_size, token, expiration_time, ip_address, metadata) 
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	QUERY_UPDATE_DOWNLOAD_COUNT = `
		UPDATE files SET download_count = download_count + 1 WHERE id = ?
	`
	QUERY_DELETE_FILE = `
		DELETE FROM files WHERE id = ?
	`
	QUERY_SELECT_METADATA = `
		SELECT metadata FROM files WHERE id = ?
	`
	QUERY_SELECT_TOKEN = `
		SELECT token FROM files WHERE id = ?
	`
	QUERY_SELECT_FILE_BY_TOKEN = `
		SELECT id FROM files WHERE token = ?
	`
	QUERY_SELECT_EXPIRED_FILES = `
		SELECT id FROM files WHERE expiration_time < CURRENT_TIMESTAMP
	`
	QUERY_SELECT_FILE_DATA = `
		SELECT id, token, file_size, metadata, expiration_time, ip_address FROM files WHERE id = ?
	`
	QUERY_UPDATE_EXPIRATION = `
		UPDATE files SET expiration_time = ? WHERE id = ?
	`
)

// NewSQLiteDB creates a new SQLiteDB from the provided configuration.
func NewSQLiteDB(config models.Configuration) *SQLiteDB {
	if config.Database.DSN != "" {
		return &SQLiteDB{
			filePath:  config.Database.DSN,
			pathIsDSN: true,
		}
	} else {
		return &SQLiteDB{
			filePath:  config.Database.FilePath,
			pathIsDSN: false,
		}
	}
}

// Connect opens the SQLite database connection.
func (db *SQLiteDB) Connect() error {
	if dir := sqliteDir(db.filePath, db.pathIsDSN); dir != "" {
		if _, err := os.Stat(dir); err != nil && errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("database directory does not exist: %w", err)
		}
	}

	var err error
	if db.pathIsDSN {
		db.sqldb, err = sqlx.Connect("sqlite", db.filePath)
	} else {
		db.sqldb, err = sqlx.Connect("sqlite", "file:"+db.filePath+"?cache=shared&mode=rwc")
	}

	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	return nil
}

// sqliteDir extracts the parent directory from a file path or SQLite DSN.
// Returns an empty string if no meaningful directory can be determined.
func sqliteDir(path string, isDSN bool) string {
	if isDSN {
		u, err := url.Parse(path)
		if err != nil || u.Path == "" {
			return ""
		}
		path = u.Path
	}
	dir := filepath.Dir(path)
	if dir == "." {
		return ""
	}

	return dir
}

// Close releases the underlying database connection.
func (db *SQLiteDB) Close() error {
	if err := db.sqldb.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	return nil
}

// Migrate applies all pending up-migrations using the embedded migration files.
func (db *SQLiteDB) Migrate() error {
	iofsSource, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	sqliteDriver, err := sqlite.WithInstance(db.sqldb.DB, &sqlite.Config{})
	if err != nil {
		return fmt.Errorf("failed to create SQLite migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", iofsSource, "sqlite", sqliteDriver)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	return nil
}

// OnFileUpload inserts a new file record with the given metadata and expiration time.
func (db *SQLiteDB) OnFileUpload(
	fileID string,
	fileHeader *multipart.FileHeader,
	token string,
	expirationTime time.Time,
	ipAddress string,
) error {
	metadata := make(map[string]string)
	if fileHeader.Header.Get("Content-Type") != "" {
		metadata["Content-Type"] = fileHeader.Header.Get("Content-Type")
	}

	jsonMetadata, err := json.Marshal(metadata)
	if err != nil {
		jsonMetadata = []byte("{}")
	}

	_, err = db.sqldb.ExecContext(
		context.Background(),
		QUERY_INSERT_FILE,
		fileID,
		fileHeader.Filename,
		fileHeader.Size,
		token,
		expirationTime,
		ipAddress,
		string(jsonMetadata))
	if err != nil {
		return fmt.Errorf("failed to insert file record: %w", err)
	}

	return nil
}

// OnFileDownload increments the download counter for the given file ID.
func (db *SQLiteDB) OnFileDownload(fileID string) error {
	result, err := db.sqldb.ExecContext(context.Background(), QUERY_UPDATE_DOWNLOAD_COUNT, fileID)
	if err != nil {
		return fmt.Errorf("failed to update download count: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for download count update: %w", err)
	}

	if rowsAffected == 0 {
		return ErrFileNotFound
	}

	return nil
}

// OnFileDelete removes the record for the given file ID from the database.
func (db *SQLiteDB) OnFileDelete(fileID string) error {
	result, err := db.sqldb.ExecContext(context.Background(), QUERY_DELETE_FILE, fileID)
	if err != nil {
		return fmt.Errorf("failed to delete file record: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for file delete: %w", err)
	}

	if rowsAffected == 0 {
		return ErrFileNotFound
	}

	return nil
}

// GetFileMetadata returns the metadata map for the given file ID.
func (db *SQLiteDB) GetFileMetadata(fileID string) (map[string]string, error) {
	var metadataStr string
	err := db.sqldb.Get(&metadataStr, QUERY_SELECT_METADATA, fileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}

		return nil, fmt.Errorf("failed to query file metadata: %w", err)
	}

	var metadata map[string]string
	err = json.Unmarshal([]byte(metadataStr), &metadata)
	if err != nil {
		return nil, fmt.Errorf("failed to parse file metadata: %w", err)
	}

	return metadata, nil
}

// GetFileToken returns the token associated with the given file ID.
func (db *SQLiteDB) GetFileToken(fileID string) (string, error) {
	var token string
	err := db.sqldb.Get(&token, QUERY_SELECT_TOKEN, fileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrFileNotFound
		}

		return "", fmt.Errorf("failed to query file token: %w", err)
	}

	return token, nil
}

// GetFileByToken returns the file ID associated with the given token.
func (db *SQLiteDB) GetFileByToken(token string) (string, error) {
	var fileID string
	err := db.sqldb.Get(&fileID, QUERY_SELECT_FILE_BY_TOKEN, token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrFileNotFound
		}

		return "", fmt.Errorf("failed to query file by token: %w", err)
	}

	return fileID, nil
}

// GetExpiredFiles returns the IDs of all files whose expiration time is in the past.
func (db *SQLiteDB) GetExpiredFiles() ([]string, error) {
	rows, err := db.sqldb.QueryContext(context.Background(), QUERY_SELECT_EXPIRED_FILES)
	if err != nil {
		return nil, fmt.Errorf("failed to query expired files: %w", err)
	}
	defer rows.Close()

	var expiredFiles []string
	for rows.Next() {
		var fileID string
		err := rows.Scan(&fileID)
		if err != nil {
			return nil, fmt.Errorf("failed to scan expired file row: %w", err)
		}
		expiredFiles = append(expiredFiles, fileID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate expired file rows: %w", err)
	}

	return expiredFiles, nil
}

// GetFileData returns the file record for the given file ID.
func (db *SQLiteDB) GetFileData(id string) (*FileRecord, error) {
	var fileRecord FileRecord
	err := db.sqldb.Get(&fileRecord, QUERY_SELECT_FILE_DATA, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}

		return nil, fmt.Errorf("failed to query file data: %w", err)
	}

	return &fileRecord, nil
}

// SetExpiration updates the expiration time for the given file ID.
func (db *SQLiteDB) SetExpiration(fileID string, expiration time.Time) error {
	result, err := db.sqldb.ExecContext(context.Background(), QUERY_UPDATE_EXPIRATION, expiration, fileID)
	if err != nil {
		return fmt.Errorf("failed to update expiration: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return ErrFileNotFound
	}

	return nil
}
