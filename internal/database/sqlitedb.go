package database

import (
	"encoding/json"
	"mime/multipart"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	"github.com/markhc/isrv/internal/models"
	_ "modernc.org/sqlite"
)

type SQLiteDB struct {
	filePath  string
	pathIsDSN bool

	sqldb *sqlx.DB
}

func NewSQLiteDB(config models.Configuration) *SQLiteDB {
	if config.Database.DSN != "" {
		return &SQLiteDB{
			filePath:  config.Database.DSN,
			pathIsDSN: true,
		}
	} else {
		return &SQLiteDB{
			filePath:  config.Database.DSN,
			pathIsDSN: false,
		}
	}
}

func (db *SQLiteDB) Connect() error {
	var err error
	if db.pathIsDSN {
		db.sqldb, err = sqlx.Connect("sqlite", db.filePath)
	} else {
		db.sqldb, err = sqlx.Connect("sqlite", "file:"+db.filePath+"?cache=shared&mode=rwc")
	}

	if err != nil {
		return err
	}

	return nil
}

func (db *SQLiteDB) Close() error {
	return db.sqldb.Close()
}

func (db *SQLiteDB) Migrate() error {
	iofsSource, err := iofs.New(migrations, "migrations")
	if err != nil {
		return err
	}

	sqliteDriver, err := sqlite.WithInstance(db.sqldb.DB, &sqlite.Config{})
	if err != nil {
		return err
	}

	m, err := migrate.NewWithInstance("iofs", iofsSource, "sqlite", sqliteDriver)
	if err != nil {
		return err
	}

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return err
	}
	return nil
}

func (db *SQLiteDB) OnFileUpload(fileID string, fileHeader *multipart.FileHeader, expirationTime time.Time, ipAddress string) error {
	metadata := make(map[string]string)
	if fileHeader.Header.Get("Content-Type") != "" {
		metadata["Content-Type"] = fileHeader.Header.Get("Content-Type")
	}

	jsonMetadata, err := json.Marshal(metadata)
	if err != nil {
		jsonMetadata = []byte("{}")
	}

	_, err = db.sqldb.Exec(`
		INSERT INTO files (id, file_name, file_size, expiration_time, ip_address, metadata) 
		VALUES (?, ?, ?, ?, ?, ?)
	`, fileID, fileHeader.Filename, fileHeader.Size, expirationTime, ipAddress, string(jsonMetadata))

	return err
}

func (db *SQLiteDB) OnFileDownload(fileID string) error {
	_, err := db.sqldb.Exec("UPDATE files SET download_count = download_count + 1 WHERE id = ?", fileID)
	return err
}

func (db *SQLiteDB) OnFileDelete(fileID string) error {
	_, err := db.sqldb.Exec("DELETE FROM files WHERE id = ?", fileID)
	return err
}

func (db *SQLiteDB) GetFileMetadata(fileID string) (map[string]string, error) {
	var metadataStr string
	err := db.sqldb.Get(&metadataStr, "SELECT metadata FROM files WHERE id = ?", fileID)
	if err != nil {
		return nil, err
	}

	var metadata map[string]string
	err = json.Unmarshal([]byte(metadataStr), &metadata)
	if err != nil {
		return nil, err
	}

	return metadata, nil
}

func (db *SQLiteDB) GetExpiredFiles() ([]string, error) {
	rows, err := db.sqldb.Query("SELECT id FROM files WHERE expiration_time < CURRENT_TIMESTAMP")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var expiredFiles []string
	for rows.Next() {
		var fileID string
		err := rows.Scan(&fileID)
		if err != nil {
			return nil, err
		}
		expiredFiles = append(expiredFiles, fileID)
	}

	return expiredFiles, nil
}
