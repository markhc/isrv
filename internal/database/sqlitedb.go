package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/telemetry"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	"github.com/uptrace/opentelemetry-go-extra/otelsqlx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
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
		INSERT INTO files (id, file_name, file_size, token, expiration_time, ip_address, content_type)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	QUERY_UPDATE_DOWNLOAD_COUNT = `
		UPDATE files SET download_count = download_count + 1 WHERE id = ?
	`
	QUERY_DELETE_FILE = `
		DELETE FROM files WHERE id = ?
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
		SELECT id, file_name, file_size, content_type, expiration_time, download_count
		FROM files WHERE id = ?
	`
	QUERY_UPDATE_EXPIRATION = `
		UPDATE files SET expiration_time = ? WHERE id = ?
	`
	QUERY_LIST_FILES_SELECT = `
		SELECT id, file_name, file_size, content_type, expiration_time,
		       download_count, ip_address, created_at
		FROM files
	`
	QUERY_LIST_FILES_COUNT = `SELECT COUNT(*) FROM files`
)

// listFilesSortColumns whitelists the columns that ListFiles may sort by,
// mapping the public sort key to the underlying SQL column.
//
//nolint:gochecknoglobals
var listFilesSortColumns = map[string]string{
	"created_at": "created_at",
	"size":       "file_size",
	"downloads":  "download_count",
	"expiration": "expiration_time",
	"name":       "file_name",
}

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
		db.sqldb, err = otelsqlx.Open("sqlite", db.filePath,
			otelsql.WithAttributes(semconv.DBSystemSqlite),
		)
	} else {
		db.sqldb, err = otelsqlx.Open("sqlite", "file:"+db.filePath+"?cache=shared&mode=rwc",
			otelsql.WithAttributes(semconv.DBSystemSqlite),
		)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	return nil
}

// sqliteDir returns the parent directory of a file path or SQLite DSN, or
// an empty string if no meaningful directory can be determined.
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

// Ping verifies the database connection is still alive.
func (db *SQLiteDB) Ping(ctx context.Context) error {
	if db.sqldb == nil {
		return fmt.Errorf("%w: not connected", ErrConnection)
	}

	if err := db.sqldb.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
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

// OnFileUpload inserts a new file record into the database.
func (db *SQLiteDB) OnFileUpload(
	ctx context.Context,
	fileID string,
	fileHeader *multipart.FileHeader,
	token string,
	expirationTime time.Time,
	ipAddress string,
) error {
	ctx, span := telemetry.Tracer().Start(ctx, "db.OnFileUpload",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
			attribute.String(telemetry.AttrFileName, fileHeader.Filename),
			attribute.Int64(telemetry.AttrFileSize, fileHeader.Size),
			attribute.String(telemetry.AttrRequestIP, ipAddress),
		),
	)

	defer span.End()

	contentType := fileHeader.Header.Get("Content-Type")

	_, err := db.sqldb.ExecContext(
		ctx,
		QUERY_INSERT_FILE,
		fileID,
		fileHeader.Filename,
		fileHeader.Size,
		token,
		expirationTime,
		ipAddress,
		contentType)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to insert file record")

		return fmt.Errorf("failed to insert file record: %w", err)
	}

	return nil
}

// OnFileDownload increments the download counter for the given file ID.
func (db *SQLiteDB) OnFileDownload(ctx context.Context, fileID string) error {
	ctx, span := telemetry.Tracer().Start(ctx, "db.OnFileDownload",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
		),
	)

	defer span.End()

	result, err := db.sqldb.ExecContext(ctx, QUERY_UPDATE_DOWNLOAD_COUNT, fileID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to update download count")

		return fmt.Errorf("failed to update download count: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get rows affected for download count update")

		return fmt.Errorf("failed to get rows affected for download count update: %w", err)
	}

	if rowsAffected == 0 {
		return ErrFileNotFound
	}

	return nil
}

// OnFileDelete removes the record for the given file ID from the database.
func (db *SQLiteDB) OnFileDelete(ctx context.Context, fileID string) error {
	ctx, span := telemetry.Tracer().Start(ctx, "db.OnFileDelete",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
		),
	)

	defer span.End()

	result, err := db.sqldb.ExecContext(ctx, QUERY_DELETE_FILE, fileID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to delete file record")

		return fmt.Errorf("failed to delete file record: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get rows affected for file delete")

		return fmt.Errorf("failed to get rows affected for file delete: %w", err)
	}

	if rowsAffected == 0 {
		return ErrFileNotFound
	}

	return nil
}

// GetFileToken returns the token associated with the given file ID.
func (db *SQLiteDB) GetFileToken(ctx context.Context, fileID string) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.GetFileToken",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
		),
	)

	defer span.End()

	var token string
	err := db.sqldb.GetContext(ctx, &token, QUERY_SELECT_TOKEN, fileID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrFileNotFound
		}

		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to query file token")

		return "", fmt.Errorf("failed to query file token: %w", err)
	}

	return token, nil
}

// GetFileByToken returns the file ID associated with the given token.
func (db *SQLiteDB) GetFileByToken(ctx context.Context, token string) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.GetFileByToken")

	defer span.End()

	var fileID string
	err := db.sqldb.GetContext(ctx, &fileID, QUERY_SELECT_FILE_BY_TOKEN, token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrFileNotFound
		}

		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to query file by token")

		return "", fmt.Errorf("failed to query file by token: %w", err)
	}

	return fileID, nil
}

// GetExpiredFiles returns the IDs of all files whose expiration time is in the past.
func (db *SQLiteDB) GetExpiredFiles(ctx context.Context) ([]string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.GetExpiredFiles")
	defer span.End()

	rows, err := db.sqldb.QueryContext(ctx, QUERY_SELECT_EXPIRED_FILES)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to query expired files")

		return nil, fmt.Errorf("failed to query expired files: %w", err)
	}
	defer rows.Close()

	var expiredFiles []string
	for rows.Next() {
		var fileID string
		err := rows.Scan(&fileID)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to scan expired file row")

			return nil, fmt.Errorf("failed to scan expired file row: %w", err)
		}
		expiredFiles = append(expiredFiles, fileID)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to iterate expired file rows")

		return nil, fmt.Errorf("failed to iterate expired file rows: %w", err)
	}

	return expiredFiles, nil
}

// GetFile returns the file record for the given file ID.
func (db *SQLiteDB) GetFile(ctx context.Context, id string) (*models.File, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.GetFile",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, id),
		),
	)
	defer span.End()

	var (
		file        models.File
		contentType sql.NullString
	)

	row := db.sqldb.QueryRowContext(ctx, QUERY_SELECT_FILE_DATA, id)
	err := row.Scan(&file.ID,
		&file.Name,
		&file.Size,
		&contentType,
		&file.Expiration,
		&file.Downloads)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrFileNotFound
		}

		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to query file data")

		return nil, fmt.Errorf("failed to query file data: %w", err)
	}

	file.ContentType = contentType.String

	return &file, nil
}

// ListFiles returns a page of file records matching filter along with the total
// number of matching records. The WHERE clause is built from filter using bound
// parameters; the ORDER BY column and direction are chosen from a whitelist to
// avoid SQL injection.
func (db *SQLiteDB) ListFiles(ctx context.Context, filter models.FileListFilter) ([]models.File, int, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.ListFiles")
	defer span.End()

	where, args := buildListFilesWhere(filter)

	var total int
	if err := db.sqldb.GetContext(ctx, &total, QUERY_LIST_FILES_COUNT+where, args...); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to count files")

		return nil, 0, fmt.Errorf("failed to count files: %w", err)
	}

	query := QUERY_LIST_FILES_SELECT + where + buildListFilesOrderBy(filter) + " LIMIT ? OFFSET ?"
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit, filter.Offset)

	rows, err := db.sqldb.QueryContext(ctx, query, args...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to query files")

		return nil, 0, fmt.Errorf("failed to query files: %w", err)
	}
	defer rows.Close()

	files := make([]models.File, 0, limit)
	for rows.Next() {
		var (
			file        models.File
			contentType sql.NullString
			ipAddress   sql.NullString
		)

		err := rows.Scan(&file.ID, &file.Name, &file.Size, &contentType,
			&file.Expiration, &file.Downloads, &ipAddress, &file.CreatedAt)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to scan file row")

			return nil, 0, fmt.Errorf("failed to scan file row: %w", err)
		}

		file.ContentType = contentType.String
		file.IPAddress = ipAddress.String
		files = append(files, file)
	}

	if err := rows.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to iterate file rows")

		return nil, 0, fmt.Errorf("failed to iterate file rows: %w", err)
	}

	return files, total, nil
}

// buildListFilesWhere builds a parameterized WHERE clause (including the leading
// " WHERE ") and its arguments from filter. It returns an empty string when no
// filters are set.
func buildListFilesWhere(filter models.FileListFilter) (string, []any) {
	var (
		conditions []string
		args       []any
	)

	if filter.Search != "" {
		conditions = append(conditions, "(file_name LIKE ? OR content_type LIKE ?)")
		pattern := "%" + filter.Search + "%"
		args = append(args, pattern, pattern)
	}

	if filter.IP != "" {
		conditions = append(conditions, "ip_address LIKE ?")
		args = append(args, "%"+filter.IP+"%")
	}

	if len(conditions) == 0 {
		return "", nil
	}

	return " WHERE " + strings.Join(conditions, " AND "), args
}

// buildListFilesOrderBy returns a safe " ORDER BY ..." clause built from the
// whitelisted sort column and direction.
func buildListFilesOrderBy(filter models.FileListFilter) string {
	column, ok := listFilesSortColumns[filter.SortBy]
	if !ok {
		column = "created_at"
	}

	direction := "DESC"
	if strings.EqualFold(filter.SortDir, "asc") {
		direction = "ASC"
	}

	return " ORDER BY " + column + " " + direction
}

// SetExpiration updates the expiration time for the given file ID.
func (db *SQLiteDB) SetExpiration(ctx context.Context, fileID string, expiration time.Time) error {
	ctx, span := telemetry.Tracer().Start(ctx, "db.SetExpiration",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
			attribute.String(telemetry.AttrFileExpiration, expiration.Format(time.RFC3339)),
		),
	)
	defer span.End()

	result, err := db.sqldb.ExecContext(ctx, QUERY_UPDATE_EXPIRATION, expiration, fileID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to update expiration")

		return fmt.Errorf("failed to update expiration: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to check rows affected")

		return fmt.Errorf("failed to check rows affected: %w", err)
	}

	if rows == 0 {
		return ErrFileNotFound
	}

	return nil
}
