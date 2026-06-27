package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime/multipart"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

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

// sqlStore implements the backend-agnostic file operations shared by all
// database/sql backends. Concrete backends (SQLiteDB, PostgresDB) embed it and
// supply their own connection and migration logic.
//
// Queries are written once using "?" placeholders; rebind translates them to
// the placeholder style of the configured driver before execution.
type sqlStore struct {
	sqldb    *sqlx.DB
	bindType int // sqlx.QUESTION, sqlx.DOLLAR, ...
}

// Close releases the underlying database connection.
func (s *sqlStore) Close() error {
	if err := s.sqldb.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	return nil
}

// Ping verifies the database connection is still alive.
func (s *sqlStore) Ping(ctx context.Context) error {
	if s.sqldb == nil {
		return fmt.Errorf("%w: not connected", ErrConnection)
	}

	if err := s.sqldb.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	return nil
}

// OnFileUpload inserts a new file record into the database.
func (s *sqlStore) OnFileUpload(
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

	_, err := s.sqldb.ExecContext(
		ctx,
		s.rebind(QUERY_INSERT_FILE),
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
func (s *sqlStore) OnFileDownload(ctx context.Context, fileID string) error {
	ctx, span := telemetry.Tracer().Start(ctx, "db.OnFileDownload",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
		),
	)

	defer span.End()

	result, err := s.sqldb.ExecContext(ctx, s.rebind(QUERY_UPDATE_DOWNLOAD_COUNT), fileID)
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
func (s *sqlStore) OnFileDelete(ctx context.Context, fileID string) error {
	ctx, span := telemetry.Tracer().Start(ctx, "db.OnFileDelete",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
		),
	)

	defer span.End()

	result, err := s.sqldb.ExecContext(ctx, s.rebind(QUERY_DELETE_FILE), fileID)
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
func (s *sqlStore) GetFileToken(ctx context.Context, fileID string) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.GetFileToken",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
		),
	)

	defer span.End()

	var token string
	err := s.sqldb.GetContext(ctx, &token, s.rebind(QUERY_SELECT_TOKEN), fileID)
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
func (s *sqlStore) GetFileByToken(ctx context.Context, token string) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.GetFileByToken")

	defer span.End()

	var fileID string
	err := s.sqldb.GetContext(ctx, &fileID, s.rebind(QUERY_SELECT_FILE_BY_TOKEN), token)
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
func (s *sqlStore) GetExpiredFiles(ctx context.Context) ([]string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.GetExpiredFiles")
	defer span.End()

	rows, err := s.sqldb.QueryContext(ctx, s.rebind(QUERY_SELECT_EXPIRED_FILES))
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
func (s *sqlStore) GetFile(ctx context.Context, id string) (*models.File, error) {
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

	row := s.sqldb.QueryRowContext(ctx, s.rebind(QUERY_SELECT_FILE_DATA), id)
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
func (s *sqlStore) ListFiles(ctx context.Context, filter models.FileListFilter) ([]models.File, int, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "db.ListFiles")
	defer span.End()

	where, args := buildListFilesWhere(filter)

	var total int
	if err := s.sqldb.GetContext(ctx, &total, s.rebind(QUERY_LIST_FILES_COUNT+where), args...); err != nil {
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

	rows, err := s.sqldb.QueryContext(ctx, s.rebind(query), args...)
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

// SetExpiration updates the expiration time for the given file ID.
func (s *sqlStore) SetExpiration(ctx context.Context, fileID string, expiration time.Time) error {
	ctx, span := telemetry.Tracer().Start(ctx, "db.SetExpiration",
		trace.WithAttributes(
			attribute.String(telemetry.AttrFileID, fileID),
			attribute.String(telemetry.AttrFileExpiration, expiration.Format(time.RFC3339)),
		),
	)
	defer span.End()

	result, err := s.sqldb.ExecContext(ctx, s.rebind(QUERY_UPDATE_EXPIRATION), expiration, fileID)
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

// rebind converts a query using "?" placeholders to the placeholder style of
// the underlying driver.
func (s *sqlStore) rebind(query string) string {
	return sqlx.Rebind(s.bindType, query)
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
