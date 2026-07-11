package database

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_buildListFilesWhere(t *testing.T) {
	tests := []struct {
		name       string
		filter     models.FileListFilter
		wantClause string
		wantArgs   []any
	}{
		{
			name:       "no filters",
			filter:     models.FileListFilter{},
			wantClause: "",
			wantArgs:   nil,
		},
		{
			name:       "search only",
			filter:     models.FileListFilter{Search: "report"},
			wantClause: " WHERE (file_name LIKE ? OR content_type LIKE ?)",
			wantArgs:   []any{"%report%", "%report%"},
		},
		{
			name:       "ip only",
			filter:     models.FileListFilter{IP: "10.0.0.1"},
			wantClause: " WHERE ip_address LIKE ?",
			wantArgs:   []any{"%10.0.0.1%"},
		},
		{
			name:       "search and ip combined",
			filter:     models.FileListFilter{Search: "pdf", IP: "192.168"},
			wantClause: " WHERE (file_name LIKE ? OR content_type LIKE ?) AND ip_address LIKE ?",
			wantArgs:   []any{"%pdf%", "%pdf%", "%192.168%"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clause, args := buildListFilesWhere(tt.filter)
			assert.Equal(t, tt.wantClause, clause)
			assert.Equal(t, tt.wantArgs, args)
		})
	}
}

func Test_buildListFilesOrderBy(t *testing.T) {
	tests := []struct {
		name   string
		filter models.FileListFilter
		want   string
	}{
		{"default when empty", models.FileListFilter{}, " ORDER BY created_at DESC"},
		{"unknown column falls back", models.FileListFilter{SortBy: "bogus"}, " ORDER BY created_at DESC"},
		{"injection attempt falls back", models.FileListFilter{SortBy: "; DROP TABLE files;--"}, " ORDER BY created_at DESC"},
		{"size column", models.FileListFilter{SortBy: "size"}, " ORDER BY file_size DESC"},
		{"downloads column", models.FileListFilter{SortBy: "downloads"}, " ORDER BY download_count DESC"},
		{"expiration column", models.FileListFilter{SortBy: "expiration"}, " ORDER BY expiration_time DESC"},
		{"name column", models.FileListFilter{SortBy: "name"}, " ORDER BY file_name DESC"},
		{"created_at column", models.FileListFilter{SortBy: "created_at"}, " ORDER BY created_at DESC"},
		{"ascending direction", models.FileListFilter{SortBy: "size", SortDir: "asc"}, " ORDER BY file_size ASC"},
		{"ascending direction case-insensitive", models.FileListFilter{SortBy: "size", SortDir: "ASC"}, " ORDER BY file_size ASC"},
		{"unknown direction falls back to desc", models.FileListFilter{SortBy: "size", SortDir: "sideways"}, " ORDER BY file_size DESC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildListFilesOrderBy(tt.filter))
		})
	}
}

func Test_rebind(t *testing.T) {
	query := "SELECT * FROM files WHERE id = ? AND token = ?"

	t.Run("question placeholders are preserved for sqlite", func(t *testing.T) {
		s := &sqlStore{bindType: sqlx.QUESTION}
		assert.Equal(t, query, s.rebind(query))
	})

	t.Run("dollar placeholders are produced for postgres", func(t *testing.T) {
		s := &sqlStore{bindType: sqlx.DOLLAR}
		assert.Equal(t, "SELECT * FROM files WHERE id = $1 AND token = $2", s.rebind(query))
	})
}

func Test_SQLiteDB_GetFileToken(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	require.NoError(t, db.OnFileUpload(ctx, &models.File{
		ID:         "tok-file",
		Name:       "tok.txt",
		Size:       10,
		Expiration: time.Now().Add(time.Hour),
		IPAddress:  "127.0.0.1",
	}, "secret-token"))

	t.Run("returns token for existing file", func(t *testing.T) {
		token, err := db.GetFileToken(ctx, "tok-file")
		require.NoError(t, err)
		assert.Equal(t, "secret-token", token)
	})

	t.Run("returns ErrFileNotFound for missing file", func(t *testing.T) {
		_, err := db.GetFileToken(ctx, "does-not-exist")
		assert.ErrorIs(t, err, ErrFileNotFound)
	})
}

func Test_SQLiteDB_GetFileByToken(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	require.NoError(t, db.OnFileUpload(ctx, &models.File{
		ID:         "by-tok-file",
		Name:       "x.txt",
		Size:       10,
		Expiration: time.Now().Add(time.Hour),
		IPAddress:  "127.0.0.1",
	}, "lookup-token"))

	t.Run("returns file ID for existing token", func(t *testing.T) {
		id, err := db.GetFileByToken(ctx, "lookup-token")
		require.NoError(t, err)
		assert.Equal(t, "by-tok-file", id)
	})

	t.Run("returns ErrFileNotFound for unknown token", func(t *testing.T) {
		_, err := db.GetFileByToken(ctx, "nope")
		assert.ErrorIs(t, err, ErrFileNotFound)
	})
}

func Test_SQLiteDB_SetExpiration(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	require.NoError(t, db.OnFileUpload(ctx, &models.File{
		ID:         "exp-file",
		Name:       "e.txt",
		Size:       10,
		Expiration: time.Now().Add(time.Hour),
		IPAddress:  "127.0.0.1",
	}, "exp-file"))

	t.Run("updates expiration for existing file", func(t *testing.T) {
		newExp := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
		require.NoError(t, db.SetExpiration(ctx, "exp-file", newExp))

		file, err := db.GetFile(ctx, "exp-file")
		require.NoError(t, err)
		assert.WithinDuration(t, newExp, file.Expiration, time.Second)
	})

	t.Run("returns ErrFileNotFound for missing file", func(t *testing.T) {
		err := db.SetExpiration(ctx, "missing", time.Now())
		assert.ErrorIs(t, err, ErrFileNotFound)
	})
}

func Test_SQLiteDB_GetFile_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, err := db.GetFile(context.Background(), "missing-id")
	assert.ErrorIs(t, err, ErrFileNotFound)
}

func Test_SQLiteDB_GetExpiredFiles_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	expired, err := db.GetExpiredFiles(context.Background())
	require.NoError(t, err)
	assert.Empty(t, expired)
}

func Test_SQLiteDB_ListFiles_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	files, total, err := db.ListFiles(context.Background(), models.FileListFilter{})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, files)
}

func Test_sqlStore_Ping(t *testing.T) {
	t.Run("succeeds on a live connection", func(t *testing.T) {
		db := setupTestDB(t)
		defer db.Close()

		require.NoError(t, db.Ping(context.Background()))
	})

	t.Run("returns ErrConnection when not connected", func(t *testing.T) {
		s := &sqlStore{}
		err := s.Ping(context.Background())
		assert.ErrorIs(t, err, ErrConnection)
	})
}

func Test_sqliteDir(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		isDSN bool
		want  string
	}{
		{"bare filename has no dir", "isrv.db", false, ""},
		{"explicit current dir", "./isrv.db", false, ""},
		{"absolute path returns parent", "/var/lib/isrv/isrv.db", false, "/var/lib/isrv"},
		{"relative nested path returns parent", "data/isrv.db", false, "data"},
		{"in-memory DSN has no dir", ":memory:", true, ""},
		{"file DSN returns parent dir", "file:/var/lib/isrv/isrv.db?cache=shared", true, "/var/lib/isrv"},
		{"DSN without path has no dir", "file:?mode=memory", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sqliteDir(tt.path, tt.isDSN))
		})
	}
}

func Test_NewSQLiteDB_FilePathAndDSN(t *testing.T) {
	t.Run("uses DSN when provided", func(t *testing.T) {
		db := NewSQLiteDB(models.Configuration{
			Database: models.DatabaseConfiguration{DSN: ":memory:"},
		})
		assert.Equal(t, ":memory:", db.filePath)
		assert.True(t, db.pathIsDSN)
	})

	t.Run("falls back to file path", func(t *testing.T) {
		db := NewSQLiteDB(models.Configuration{
			Database: models.DatabaseConfiguration{FilePath: "/tmp/isrv.db"},
		})
		assert.Equal(t, "/tmp/isrv.db", db.filePath)
		assert.False(t, db.pathIsDSN)
	})
}

func Test_SQLiteDB_Connect_MissingDirectory(t *testing.T) {
	db := NewSQLiteDB(models.Configuration{
		Database: models.DatabaseConfiguration{FilePath: "/nonexistent-isrv-dir/does/not/exist/isrv.db"},
	})

	err := db.Connect()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database directory does not exist")
}

func Test_SQLiteDB_Connect_FilePath(t *testing.T) {
	dir := t.TempDir()
	db := NewSQLiteDB(models.Configuration{
		Database: models.DatabaseConfiguration{FilePath: dir + "/isrv.db"},
	})

	require.NoError(t, db.Connect())
	require.NoError(t, db.Migrate())
	assert.NoError(t, db.Close())
}
