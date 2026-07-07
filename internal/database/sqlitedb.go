package database

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	"github.com/markhc/isrv/internal/models"
	_ "modernc.org/sqlite"
)

// SQLiteDB implements Database using a SQLite backend. The backend-agnostic
// file operations are provided by the embedded sqlStore.
type SQLiteDB struct {
	sqlStore

	filePath  string
	pathIsDSN bool
}

// NewSQLiteDB creates a new SQLiteDB from the provided configuration.
func NewSQLiteDB(config models.Configuration) *SQLiteDB {
	db := &SQLiteDB{
		sqlStore: sqlStore{bindType: sqlx.QUESTION},
	}

	if config.Database.DSN != "" {
		db.filePath = config.Database.DSN
		db.pathIsDSN = true
	} else {
		db.filePath = config.Database.FilePath
	}

	return db
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
		db.sqldb, err = sqlx.Open("sqlite", db.filePath)
	} else {
		db.sqldb, err = sqlx.Open("sqlite", "file:"+db.filePath+"?cache=shared&mode=rwc")
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
