package database

import (
	"context"
	"mime/multipart"
	"net/textproto"
	"testing"
	"time"

	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedFile inserts a file record for ListFiles tests.
func seedFile(t *testing.T, db *SQLiteDB, id, name, contentType, ip string, size int64) {
	t.Helper()

	header := &multipart.FileHeader{
		Filename: name,
		Size:     size,
		Header:   make(textproto.MIMEHeader),
	}
	if contentType != "" {
		header.Header.Set("Content-Type", contentType)
	}

	err := db.OnFileUpload(context.Background(), id, header, id, time.Now().Add(24*time.Hour), ip)
	require.NoError(t, err)
}

func Test_SQLiteDB_ListFiles(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	seedFile(t, db, "id-1", "report.pdf", "application/pdf", "192.168.1.10", 100)
	seedFile(t, db, "id-2", "photo.png", "image/png", "192.168.1.10", 300)
	seedFile(t, db, "id-3", "notes.txt", "text/plain", "10.0.0.5", 200)

	ctx := context.Background()

	t.Run("no filter returns all", func(t *testing.T) {
		files, total, err := db.ListFiles(ctx, models.FileListFilter{})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Len(t, files, 3)
	})

	t.Run("filter by IP substring", func(t *testing.T) {
		files, total, err := db.ListFiles(ctx, models.FileListFilter{IP: "192.168.1.10"})
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.Len(t, files, 2)
		for _, f := range files {
			assert.Equal(t, "192.168.1.10", f.IPAddress)
		}
	})

	t.Run("search matches filename", func(t *testing.T) {
		files, total, err := db.ListFiles(ctx, models.FileListFilter{Search: "photo"})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		require.Len(t, files, 1)
		assert.Equal(t, "photo.png", files[0].Name)
	})

	t.Run("search matches content type", func(t *testing.T) {
		files, total, err := db.ListFiles(ctx, models.FileListFilter{Search: "image/"})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		require.Len(t, files, 1)
		assert.Equal(t, "id-2", files[0].ID)
	})

	t.Run("sort by size ascending", func(t *testing.T) {
		files, _, err := db.ListFiles(ctx, models.FileListFilter{SortBy: "size", SortDir: "asc"})
		require.NoError(t, err)
		require.Len(t, files, 3)
		assert.Equal(t, int64(100), files[0].Size)
		assert.Equal(t, int64(300), files[2].Size)
	})

	t.Run("pagination limits and reports total", func(t *testing.T) {
		page1, total, err := db.ListFiles(ctx, models.FileListFilter{
			SortBy: "size", SortDir: "asc", Limit: 2, Offset: 0,
		})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		require.Len(t, page1, 2)

		page2, _, err := db.ListFiles(ctx, models.FileListFilter{
			SortBy: "size", SortDir: "asc", Limit: 2, Offset: 2,
		})
		require.NoError(t, err)
		require.Len(t, page2, 1)
		assert.Equal(t, int64(300), page2[0].Size)
	})

	t.Run("invalid sort column falls back to default", func(t *testing.T) {
		_, _, err := db.ListFiles(ctx, models.FileListFilter{SortBy: "; DROP TABLE files;--"})
		require.NoError(t, err)

		// The table must still exist and contain all records.
		_, total, err := db.ListFiles(ctx, models.FileListFilter{})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
	})
}
