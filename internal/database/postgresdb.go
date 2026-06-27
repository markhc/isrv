package database

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/markhc/isrv/internal/models"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	"github.com/uptrace/opentelemetry-go-extra/otelsqlx"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// PostgresDB implements Database using a PostgreSQL backend. The
// backend-agnostic file operations are provided by the embedded sqlStore.
type PostgresDB struct {
	sqlStore

	dsn string
}

// NewPostgresDB creates a new PostgresDB from the provided configuration.
func NewPostgresDB(config models.Configuration) *PostgresDB {
	return &PostgresDB{
		sqlStore: sqlStore{bindType: sqlx.DOLLAR},
		dsn:      postgresDSN(config.Database),
	}
}

// postgresDSN returns the connection string for the database. When an explicit
// DSN is configured it is used as-is; otherwise a URL is assembled from the
// individual connection parameters.
func postgresDSN(config models.DatabaseConfiguration) string {
	if config.DSN != "" {
		return config.DSN
	}

	host := config.Host
	if config.Port != 0 {
		host = host + ":" + strconv.Itoa(config.Port)
	}

	u := url.URL{
		Scheme: "postgres",
		Host:   host,
		Path:   "/" + config.DBName,
	}

	if config.User != "" {
		if config.Password != "" {
			u.User = url.UserPassword(config.User, config.Password)
		} else {
			u.User = url.User(config.User)
		}
	}

	return u.String()
}

// Connect opens the PostgreSQL database connection.
func (db *PostgresDB) Connect() error {
	sqldb, err := otelsqlx.Open("pgx", db.dsn,
		otelsql.WithAttributes(semconv.DBSystemPostgreSQL),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	db.sqldb = sqldb

	return nil
}

// Migrate applies all pending up-migrations using the embedded migration files.
func (db *PostgresDB) Migrate() error {
	iofsSource, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	pgDriver, err := pgxmigrate.WithInstance(db.sqldb.DB, &pgxmigrate.Config{})
	if err != nil {
		return fmt.Errorf("failed to create PostgreSQL migration driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", iofsSource, "pgx5", pgDriver)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	return nil
}
